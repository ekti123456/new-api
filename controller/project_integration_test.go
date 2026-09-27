package controller

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Queries use only these existing columns. No production migration is added.
type projectChannelFixture struct {
	ID          int `gorm:"primaryKey"`
	Name        string
	Key         string
	BaseURL     string
	Status      int
	ChannelInfo string
}

func (projectChannelFixture) TableName() string { return "channels" }

type projectLogFixture struct {
	ID        int `gorm:"primaryKey"`
	ChannelID int
	Type      int
	CreatedAt int64
	Quota     int64
}

func (projectLogFixture) TableName() string { return "logs" }

func TestProjectIntegrationSummaryIsolation(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var mainDriver, logDriver gorm.Dialector
			switch engine {
			case "sqlite":
				mainDriver = sqlite.Open(filepath.Join(t.TempDir(), "main.db"))
				logDriver = sqlite.Open(filepath.Join(t.TempDir(), "logs.db"))
			case "mysql":
				if os.Getenv("PROJECT_TEST_MYSQL_DSN") == "" {
					t.Skip("PROJECT_TEST_MYSQL_DSN and PROJECT_TEST_MYSQL_LOG_DSN not configured")
				}
				require.Contains(t, os.Getenv("PROJECT_TEST_MYSQL_DSN"), "project")
				require.Contains(t, os.Getenv("PROJECT_TEST_MYSQL_LOG_DSN"), "project")
				mainDriver = mysql.Open(os.Getenv("PROJECT_TEST_MYSQL_DSN"))
				logDriver = mysql.Open(os.Getenv("PROJECT_TEST_MYSQL_LOG_DSN"))
			case "postgres":
				if os.Getenv("PROJECT_TEST_POSTGRES_DSN") == "" {
					t.Skip("PROJECT_TEST_POSTGRES_DSN and PROJECT_TEST_POSTGRES_LOG_DSN not configured")
				}
				require.Contains(t, os.Getenv("PROJECT_TEST_POSTGRES_DSN"), "project")
				require.Contains(t, os.Getenv("PROJECT_TEST_POSTGRES_LOG_DSN"), "project")
				mainDriver = postgres.Open(os.Getenv("PROJECT_TEST_POSTGRES_DSN"))
				logDriver = postgres.Open(os.Getenv("PROJECT_TEST_POSTGRES_LOG_DSN"))
			}
			mainDB, err := gorm.Open(mainDriver, &gorm.Config{})
			require.NoError(t, err)
			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			versionQuery := "SELECT version()"
			if engine == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, mainDB.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database: %s; independent log database enabled", version)
			oldDB, oldLogs := model.DB, model.LOG_DB
			model.DB, model.LOG_DB = mainDB, logDB
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogs
				sqlDB, _ := mainDB.DB()
				_ = sqlDB.Close()
				sqlLog, _ := logDB.DB()
				_ = sqlLog.Close()
			})
			require.NoError(t, mainDB.AutoMigrate(&projectChannelFixture{}))
			require.NoError(t, logDB.AutoMigrate(&projectLogFixture{}))
			require.NoError(t, mainDB.Where("id BETWEEN ? AND ?", 910001, 910010).Delete(&projectChannelFixture{}).Error)
			require.NoError(t, logDB.Where("id BETWEEN ? AND ?", 910001, 910010).Delete(&projectLogFixture{}).Error)
			now := time.Now().Unix()
			secret := strings.Repeat("project-test-secret", 3)
			key := "sk-project-bound-key"
			digest := sha256.Sum256([]byte(key))
			fp := hex.EncodeToString(digest[:])
			t.Setenv("CODEX2API_POLICY_ENABLED", "true")
			t.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "true")
			binding := []map[string]any{{"platform_id": "project-test", "target": "https://cpa.test/service", "codex_key_fingerprint": fp, "secret": secret, "enabled": true}}
			encoded, err := common.Marshal(binding)
			require.NoError(t, err)
			t.Setenv("CODEX2API_POLICY_BINDINGS", string(encoded))
			channels := []projectChannelFixture{
				{910001, "authorized", key, "https://cpa.test/service", 1, "{}"},
				{910002, "other-key", "sk-other", "https://cpa.test/service", 1, "{}"},
				{910003, "other-target", key, "https://unrelated.test", 1, "{}"},
				{910004, "mixed-keys", key + "\nsk-other", "https://cpa.test/service", 1, "{}"},
				{910005, "prefix-collision", key, "https://cpa.test/service-evil", 1, "{}"},
			}
			require.NoError(t, mainDB.Create(&channels).Error)
			logs := []projectLogFixture{{910001, 910001, model.LogTypeConsume, now - 5, 500000}, {910002, 910001, model.LogTypeConsume, now - 70, 250000}, {910003, 910002, model.LogTypeConsume, now - 2, 9000000}, {910004, 910004, model.LogTypeConsume, now - 2, 9000000}, {910005, 910001, model.LogTypeError, now - 1, 100}, {910006, 910001, model.LogTypeConsume, now - 90000, 100}}
			require.NoError(t, logDB.Create(&logs).Error)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/api/integration/codex2api/summary", GetCodexProjectSummary)
			makeRequest := func(timestamp int64) *http.Request {
				nonce := make([]byte, 16)
				_, err := rand.Read(nonce)
				require.NoError(t, err)
				r := httptest.NewRequest("GET", "/api/integration/codex2api/summary", nil)
				fields := []string{strconv.FormatInt(timestamp, 10), hex.EncodeToString(nonce), "project-test", fp, strconv.FormatInt(now-3600, 10)}
				for i, n := range []string{"Timestamp", "Nonce", "Platform", "Key-Fingerprint", "Day-Start"} {
					r.Header.Set("X-CPA-"+n, fields[i])
				}
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write([]byte("project-summary-v1\nGET\n/api/integration/codex2api/summary\n" + strings.Join(fields, "\n")))
				r.Header.Set("X-CPA-Signature", hex.EncodeToString(mac.Sum(nil)))
				return r
			}
			r := makeRequest(now)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, 200, w.Code, w.Body.String())
			var result struct {
				RPM      int64 `json:"rpm"`
				Quota    int64 `json:"quota"`
				Requests int64 `json:"requests"`
				Channels []struct {
					ID int `json:"id"`
				} `json:"channels"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
			assert.Equal(t, int64(1), result.RPM)
			assert.Equal(t, int64(750000), result.Quota)
			assert.Equal(t, int64(2), result.Requests)
			require.Len(t, result.Channels, 1)
			assert.Equal(t, 910001, result.Channels[0].ID)
			assert.NotContains(t, w.Body.String(), key)
			assert.NotContains(t, w.Body.String(), secret)
			assert.NotContains(t, w.Body.String(), "other-key")
			w = httptest.NewRecorder()
			router.ServeHTTP(w, r)
			assert.Equal(t, 401, w.Code, "replay must fail")
			for _, kind := range []string{"expired", "tampered-window", "wrong-purpose", "missing-signature", "wrong-key"} {
				r = makeRequest(now)
				switch kind {
				case "expired":
					r = makeRequest(now - 120)
				case "tampered-window":
					r.Header.Set("X-CPA-Day-Start", strconv.FormatInt(now-100, 10))
				case "wrong-purpose":
					r.Header.Set("X-CPA-Signature", strings.Repeat("00", 32))
				case "missing-signature":
					r.Header.Del("X-CPA-Signature")
				case "wrong-key":
					r.Header.Set("X-CPA-Key-Fingerprint", strings.Repeat("00", 32))
				}
				w = httptest.NewRecorder()
				router.ServeHTTP(w, r)
				assert.Equal(t, 401, w.Code, kind)
			}
			t.Setenv("CODEX2API_POLICY_ENABLED", "false")
			w = httptest.NewRecorder()
			router.ServeHTTP(w, makeRequest(now))
			assert.Equal(t, 401, w.Code, "disabled binding cannot read")
		})
	}
}
