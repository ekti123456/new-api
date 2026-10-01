package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSavedAuditPolicyAppliesToNextRequestWithoutChangingInflightPolicy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "policy-settings.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	previousDB := model.DB
	model.DB = db
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		model.DB = previousDB
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		conn, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})
	t.Setenv("CODEX2API_POLICY_ENABLED", "false")
	key := "sk-policy-runtime-test"
	digest := sha256.Sum256([]byte(key))
	settings := NewAPIPolicySettings{Enabled: true, IdentityForwardEnabled: true, AuditEnabled: true, BanAfter: 2, WindowSeconds: 86400,
		Bindings: []newAPIPolicyBinding{{Target: "https://codex.example", PlatformID: "runtime-test", Secret: strings.Repeat("s", 32), CodexKeyFingerprint: hex.EncodeToString(digest[:]), Enabled: true}},
	}
	save := func() {
		require.NoError(t, settings.Normalize())
		raw, err := common.Marshal(settings)
		require.NoError(t, err)
		require.NoError(t, model.UpdateOptionsBulk(map[string]string{NewAPIPolicyOption: string(raw)}))
	}
	request := func(id string) *http.Request {
		body := []byte(`{"model":"gpt-6-astra","input":"ordinary request"}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.RemoteAddr = "203.0.113.8:1234"
		req, err := http.NewRequest(http.MethodPost, "https://codex.example/v1/responses", bytes.NewReader(body))
		require.NoError(t, err)
		info := &relaycommon.RelayInfo{UserId: 42, RequestId: id, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ApiKey: key}}
		require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
		return req
	}
	save()
	first := request("audit-before-save")
	firstPolicy := first.Context().Value(newAPIPolicyRequestContextKey{}).(newAPIPolicyRequestContext)
	assert.NotEmpty(t, first.Header.Get("X-NewAPI-Signature"))
	assert.False(t, firstPolicy.Enforcement.StrikeEnabled)
	settings.StrikeEnabled, settings.AccountBanEnabled = true, true
	settings.BanAfter = 3
	save()
	next := request("audit-after-save")
	nextPolicy := next.Context().Value(newAPIPolicyRequestContextKey{}).(newAPIPolicyRequestContext)
	assert.True(t, nextPolicy.Enforcement.StrikeEnabled)
	assert.True(t, nextPolicy.Enforcement.AccountBanEnabled)
	assert.Equal(t, 3, nextPolicy.Enforcement.BanAfter)
	assert.False(t, first.Context().Value(newAPIPolicyRequestContextKey{}).(newAPIPolicyRequestContext).Enforcement.StrikeEnabled)
	settings.Enabled = false
	save()
	disabled := request("audit-after-disable")
	assert.Empty(t, disabled.Header.Get("X-NewAPI-Signature"))
	assert.Nil(t, disabled.Context().Value(newAPIPolicyRequestContextKey{}))
}
