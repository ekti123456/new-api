package controller

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/requestlimit"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestIndependentRateSettingsDatabaseMatrix(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var driver gorm.Dialector
			switch engine {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "independent.db"))
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
			t.Cleanup(func() { conn.Close() })
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}))
			oldDB, oldRedis := model.DB, common.RedisEnabled
			model.DB, common.RedisEnabled = db, false
			t.Cleanup(func() { model.DB, common.RedisEnabled = oldDB, oldRedis })
			common.OptionMapRWMutex.Lock()
			oldOptions := common.OptionMap
			common.OptionMap = map[string]string{}
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = oldOptions
				common.OptionMapRWMutex.Unlock()
			})
			user := model.User{Username: "rpm-" + uuid.NewString()[:8], DisplayName: "Rate test"}
			require.NoError(t, db.Create(&user).Error)
			t.Cleanup(func() { db.Unscoped().Delete(&user) })
			do := func(method, path string, body any, handler gin.HandlerFunc) *httptest.ResponseRecorder {
				raw, err := common.Marshal(body)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(method, path, strings.NewReader(string(raw)))
				handler(c)
				return w
			}
			decode := func(w *httptest.ResponseRecorder) independentRateEditor {
				t.Helper()
				var response struct {
					Success bool                  `json:"success"`
					Data    independentRateEditor `json:"data"`
				}
				require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
				require.True(t, response.Success, w.Body.String())
				return response.Data
			}
			initial := decode(do("GET", "/", nil, GetIndependentRateLimits))
			initial.Settings.Enabled = true
			initial.Settings.Rules = []setting.IndependentRateRule{{ID: "rule", Name: strings.Repeat("中文限流", 10), Enabled: true, Path: "/v1/chat/completions", UAMode: "any", Stream: "non_stream", Limit: 100, Overrides: []setting.UserRateOverride{{UserID: user.Id, Limit: 200}}}}
			saved := decode(do("PUT", "/", initial, SaveIndependentRateLimits))
			require.NotEmpty(t, saved.Settings.Namespace)
			require.Equal(t, initial.Settings.Rules[0].Name, saved.Settings.Rules[0].Name)
			require.Equal(t, 409, do("PUT", "/", initial, SaveIndependentRateLimits).Code)
			var persisted model.Option
			require.NoError(t, db.Where(&model.Option{Key: setting.IndependentRateLimitOption}).First(&persisted).Error)
			// Simulate a database reload after restart, preserving both namespace and rules.
			common.OptionMapRWMutex.Lock()
			common.OptionMap = map[string]string{persisted.Key: persisted.Value}
			common.OptionMapRWMutex.Unlock()
			reloaded := decode(do("GET", "/", nil, GetIndependentRateLimits))
			require.Equal(t, saved, reloaded)
			usage, err := requestlimit.Default.Check(context.Background(), saved.Settings.Namespace, "rule", user.Id, 200, true)
			require.NoError(t, err)
			require.Equal(t, 1, usage.Count)
			w := do("GET", "/?rule_id=rule&query="+user.Username, nil, GetIndependentRateUsage)
			require.Contains(t, w.Body.String(), `"count":1`)
			require.Contains(t, w.Body.String(), `"limit":200`)
			require.Contains(t, w.Body.String(), user.Username)
			saved.Settings.Rules[0].Overrides[0].Limit = 1
			saved = decode(do("PUT", "/", saved, SaveIndependentRateLimits))
			usage, err = requestlimit.Default.Check(context.Background(), saved.Settings.Namespace, "rule", user.Id, 1, true)
			require.NoError(t, err)
			require.False(t, usage.Allowed, "save must not reset the user's counter")
			saved.Settings.Enabled = false
			saved = decode(do("PUT", "/", saved, SaveIndependentRateLimits))
			require.False(t, saved.Settings.Enabled)
			require.Equal(t, reloaded.Settings.Namespace, saved.Settings.Namespace)
			bad := saved
			bad.Settings.Rules = append([]setting.IndependentRateRule(nil), saved.Settings.Rules...)
			bad.Settings.Rules[0].Path = "/v1/*"
			w = do("PUT", "/", bad, SaveIndependentRateLimits)
			require.Contains(t, w.Body.String(), `"success":false`)
			require.Equal(t, saved, decode(do("GET", "/", nil, GetIndependentRateLimits)))
			w = do("PUT", "/", OptionUpdateRequest{Key: setting.IndependentRateLimitOption, Value: "{}"}, UpdateOption)
			require.Contains(t, w.Body.String(), "independent rate limits editor")
		})
	}
}
