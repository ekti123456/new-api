package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appI18n "github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCodexRootDistributorTest(t *testing.T) (*model.Channel, string, string) {
	t.Helper()
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	require.NoError(t, appI18n.Init())

	baseURL := "https://codex-root.example"
	key := "sk-pinned-root-key"
	priority := int64(1)
	channel := &model.Channel{
		Id:       98101,
		Type:     constant.ChannelTypeOpenAI,
		Key:      key,
		Status:   common.ChannelStatusEnabled,
		Name:     "codex-root-channel",
		BaseURL:  &baseURL,
		Models:   "gpt-5.6-sol",
		Group:    "pro",
		Priority: &priority,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "pro", Model: "gpt-5.6-sol", ChannelId: channel.Id, Enabled: true, Priority: &priority,
	}).Error)
	model.InitChannelCache()

	keyDigest := sha256.Sum256([]byte(key))
	keyFingerprint := hex.EncodeToString(keyDigest[:])
	t.Setenv("CODEX2API_POLICY_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_BINDINGS", `[{"platform_id":"newapi","target":"`+baseURL+`","codex_key_fingerprint":"`+keyFingerprint+`","secret":"0123456789abcdef0123456789abcdef","enabled":true}]`)
	t.Setenv("CODEX2API_POLICY_SECRET", "")
	t.Setenv("CODEX2API_POLICY_TARGETS", "")

	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled && originalDB != nil &&
			originalDB.Migrator().HasTable(&model.Channel{}) && originalDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return channel, key, keyFingerprint
}

func codexRootDistributorContext(userID int) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyUserId, userID)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "pro")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "pro")
	return c
}

func codexRootDistributorRequestContext(userID, channelID int, rootID, leafID string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := strings.NewReader(`{"model":"gpt-5.6-luna","input":"review"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", body)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Session-Id", rootID)
	c.Request.Header.Set("Thread-Id", leafID)
	c.Request.Header.Set("X-Client-Request-Id", leafID)
	c.Request.Header.Set("X-Codex-Window-Id", leafID+":1")
	c.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)
	common.SetContextKey(c, constant.ContextKeyUserId, userID)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "pro")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "pro")
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(channelID))
	return c, recorder
}

func TestPrepareCodexRootChannelRouteAllowsOnlyPassiveModelWithoutPublishingAbility(t *testing.T) {
	channel, _, keyFingerprint := setupCodexRootDistributorTest(t)
	rootID := "root:" + t.Name()
	require.NoError(t, service.StoreCodexRootChannelBinding(42, rootID, service.CodexRootChannelBinding{
		ChannelID: channel.Id, SelectedGroup: "pro", KeyIndex: 0, KeyFingerprint: keyFingerprint,
	}))
	resolution := relaychannel.CodexRootSessionResolution{RootID: rootID, Resolved: true, Related: true}

	for _, passiveModel := range []string{"gpt-5.6-luna", "codex-auto-review"} {
		t.Run(passiveModel, func(t *testing.T) {
			require.False(t, model.IsChannelEnabledForGroupModel("pro", passiveModel, channel.Id))
			c := codexRootDistributorContext(42)
			selected, group, found, err := prepareCodexRootChannelRoute(c, resolution, passiveModel, "pro")
			require.NoError(t, err)
			require.True(t, found)
			require.NotNil(t, selected)
			require.Equal(t, channel.Id, selected.Id)
			require.Equal(t, "pro", group)
			require.True(t, common.GetContextKeyBool(c, constant.ContextKeyCodexRootChannelPinned))
			require.False(t, model.IsChannelEnabledForGroupModel("pro", passiveModel, channel.Id), "passive routing must not publish internal models in channel abilities")
		})
	}
}

func TestPrepareCodexRootChannelRouteRejectsDirectAndUnlistedOrdinaryModels(t *testing.T) {
	channel, _, keyFingerprint := setupCodexRootDistributorTest(t)
	rootID := "root:" + t.Name()
	require.NoError(t, service.StoreCodexRootChannelBinding(42, rootID, service.CodexRootChannelBinding{
		ChannelID: channel.Id, SelectedGroup: "pro", KeyIndex: 0, KeyFingerprint: keyFingerprint,
	}))

	direct := relaychannel.CodexRootSessionResolution{RootID: rootID, Resolved: true, Related: false}
	selected, _, found, err := prepareCodexRootChannelRoute(codexRootDistributorContext(42), direct, "gpt-5.6-luna", "pro")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, selected)

	related := relaychannel.CodexRootSessionResolution{RootID: rootID, Resolved: true, Related: true}
	selected, _, found, err = prepareCodexRootChannelRoute(codexRootDistributorContext(42), related, "gpt-5.6-terra", "pro")
	require.Error(t, err)
	require.True(t, found)
	require.Nil(t, selected)
}

func TestPrepareCodexRootChannelRouteFailsClosedWhenPinnedKeyChanges(t *testing.T) {
	channel, _, keyFingerprint := setupCodexRootDistributorTest(t)
	rootID := "root:" + t.Name()
	require.NoError(t, service.StoreCodexRootChannelBinding(42, rootID, service.CodexRootChannelBinding{
		ChannelID: channel.Id, SelectedGroup: "pro", KeyIndex: 0, KeyFingerprint: keyFingerprint,
	}))
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", "changed-key").Error)
	model.InitChannelCache()

	resolution := relaychannel.CodexRootSessionResolution{RootID: rootID, Resolved: true, Related: true}
	selected, _, found, err := prepareCodexRootChannelRoute(codexRootDistributorContext(42), resolution, "gpt-5.6-luna", "pro")
	require.Error(t, err)
	require.True(t, found)
	require.Nil(t, selected)
}

func TestDistributorPinsTokenSpecificDerivedRequestToRootChannelAndKey(t *testing.T) {
	channel, key, keyFingerprint := setupCodexRootDistributorTest(t)
	rootID := "01a031a2-043b-7f42-afa6-ce5491d9be64"
	leafID := "01a031a2-ca1e-7063-8ba7-f140c182c629"
	require.NoError(t, service.StoreCodexRootChannelBinding(42, rootID, service.CodexRootChannelBinding{
		ChannelID: channel.Id, SelectedGroup: "pro", KeyIndex: 0, KeyFingerprint: keyFingerprint,
	}))

	c, recorder := codexRootDistributorRequestContext(42, channel.Id, rootID, leafID)
	Distribute()(c)
	require.Less(t, recorder.Code, http.StatusBadRequest)
	require.Equal(t, channel.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
	require.Equal(t, key, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyCodexRootChannelPinned))
}

func TestDistributorRejectsTokenSpecificChannelThatDiffersFromRootBinding(t *testing.T) {
	channel, _, keyFingerprint := setupCodexRootDistributorTest(t)
	other := &model.Channel{
		Id: channel.Id + 1, Type: constant.ChannelTypeOpenAI, Key: "other-key",
		Status: common.ChannelStatusEnabled, Name: "other-channel", Models: "gpt-5.6-sol", Group: "pro",
	}
	require.NoError(t, model.DB.Create(other).Error)
	rootID := "01a031a2-043b-7f42-afa6-ce5491d9be64"
	leafID := "01a031a2-ca1e-7063-8ba7-f140c182c629"
	require.NoError(t, service.StoreCodexRootChannelBinding(42, rootID, service.CodexRootChannelBinding{
		ChannelID: channel.Id, SelectedGroup: "pro", KeyIndex: 0, KeyFingerprint: keyFingerprint,
	}))

	c, recorder := codexRootDistributorRequestContext(42, other.Id, rootID, leafID)
	Distribute()(c)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.True(t, c.IsAborted())
}
