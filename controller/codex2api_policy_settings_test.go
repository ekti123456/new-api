package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCodexAuditStatusResolvesOnlyEnabledBoundChannelKeys(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit-channels.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		conn, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})
	key, rightURL, wrongURL := "sk-audit-bound-test", "https://codex.example/proxy", "https://unrelated.example/proxy"
	digest := sha256.Sum256([]byte(key))
	binding := channel.NewAPIPolicyBinding{Target: rightURL + "/v1", CodexKeyFingerprint: hex.EncodeToString(digest[:])}
	rows := []model.Channel{
		{Id: 1, Key: key, BaseURL: &wrongURL, Status: common.ChannelStatusEnabled},
		{Id: 2, Key: "sk-other", BaseURL: &rightURL, Status: common.ChannelStatusEnabled},
		{Id: 3, Key: key, BaseURL: &rightURL, Status: common.ChannelStatusManuallyDisabled},
		{Id: 4, Key: "sk-other\n" + key, BaseURL: &rightURL, Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&rows).Error)
	assert.Equal(t, key, codexPolicyChannelKey(context.Background(), binding))
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 4).Update("status", common.ChannelStatusManuallyDisabled).Error)
	assert.Empty(t, codexPolicyChannelKey(context.Background(), binding))
}

func TestCodexAuditEditorPersistsRedactsAndReloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var driver gorm.Dialector
			switch engine {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "policy.db"))
			case "mysql":
				dsn := os.Getenv("PROJECT_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("PROJECT_TEST_MYSQL_DSN not configured")
				}
				require.Contains(t, dsn, "project")
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("PROJECT_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("PROJECT_TEST_POSTGRES_DSN not configured")
				}
				require.Contains(t, dsn, "project")
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			conn, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			require.NoError(t, db.AutoMigrate(&model.Option{}))
			oldDB := model.DB
			model.DB = db
			t.Cleanup(func() { model.DB = oldDB })
			common.OptionMapRWMutex.Lock()
			oldMap := common.OptionMap
			common.OptionMap = map[string]string{}
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = oldMap; common.OptionMapRWMutex.Unlock() })
			t.Setenv("CODEX2API_POLICY_ENABLED", "false")
			t.Setenv("CODEX2API_POLICY_BINDINGS", "")
			t.Setenv("CODEX2API_POLICY_TARGETS", "")
			t.Setenv("CODEX2API_POLICY_SECRET", "")
			secret := strings.Repeat("audit-test-secret-", 3)
			key := "sk-editor-only-test"
			old, source, revision, err := channel.ReadNewAPIPolicySettings()
			require.NoError(t, err)
			editor := redactedCodexPolicyEditor(old, source, revision)
			editor.Settings.Enabled = true
			editor.Connections = []codexConnectionEditor{{NewAPIPolicyBinding: channel.NewAPIPolicyBinding{Target: "https://c2.example", PlatformID: "newapi-a", Secret: secret, Enabled: true}, APIKey: key}}
			do := func(method string, body any, handler gin.HandlerFunc) *httptest.ResponseRecorder {
				raw, err := common.Marshal(body)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(method, "/api/option/codex2api-policy", strings.NewReader(string(raw)))
				handler(c)
				return w
			}
			w := do("PUT", editor, SaveCodex2APIPolicySettings)
			require.Equal(t, 200, w.Code, w.Body.String())
			assert.NotContains(t, w.Body.String(), secret)
			assert.NotContains(t, w.Body.String(), key)
			var envelope struct {
				Success bool              `json:"success"`
				Data    codexPolicyEditor `json:"data"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
			require.True(t, envelope.Success, w.Body.String())
			assert.True(t, envelope.Data.Connections[0].SecretConfigured)
			assert.Equal(t, 409, do("PUT", editor, SaveCodex2APIPolicySettings).Code, "stale editor cannot replace a newer snapshot")
			editor = envelope.Data
			editor.Settings.AuditEnabled = false
			w = do("PUT", editor, SaveCodex2APIPolicySettings)
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
			require.True(t, envelope.Success, w.Body.String())
			stored, _, _, err := channel.ReadNewAPIPolicySettings()
			require.NoError(t, err)
			assert.Equal(t, secret, stored.Bindings[0].Secret)
			assert.False(t, stored.AuditEnabled)
			var option model.Option
			require.NoError(t, db.Where(&model.Option{Key: channel.NewAPIPolicyOption}).First(&option).Error)
			assert.NotContains(t, option.Value, key, "raw calling key is not stored")
			common.OptionMapRWMutex.Lock()
			common.OptionMap = map[string]string{channel.NewAPIPolicyOption: option.Value}
			common.OptionMapRWMutex.Unlock()
			stored, _, _, err = channel.ReadNewAPIPolicySettings()
			require.NoError(t, err)
			assert.True(t, stored.Enabled)
			assert.Equal(t, secret, stored.Bindings[0].Secret)
			w = do("GET", nil, GetOptions)
			assert.NotContains(t, w.Body.String(), secret)
			assert.NotContains(t, w.Body.String(), channel.NewAPIPolicyOption)
			w = do("PUT", OptionUpdateRequest{Key: channel.NewAPIPolicyOption, Value: "{}"}, UpdateOption)
			assert.Contains(t, w.Body.String(), "audit settings editor")
			editor = envelope.Data
			editor.Connections[0].Target = "https://other.example"
			w = do("PUT", editor, SaveCodex2APIPolicySettings)
			assert.Contains(t, w.Body.String(), "re-enter audit secret")
			editor = envelope.Data
			editor.Connections[0].Target = "https://c2.example"
			editor.Settings.Enabled = false
			w = do("PUT", editor, SaveCodex2APIPolicySettings)
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
			require.True(t, envelope.Success)
			w = do(http.MethodGet, nil, GetCodex2APIConnectionStatus)
			assert.Contains(t, w.Body.String(), "disabled")
		})
	}
}
