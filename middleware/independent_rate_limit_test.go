package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/requestlimit"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func independentTestSettings(t *testing.T) setting.IndependentRateLimits {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	oldRedis, oldEnabled := common.RedisEnabled, setting.ModelRequestRateLimitEnabled
	common.RedisEnabled, setting.ModelRequestRateLimitEnabled = false, false
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
		common.RedisEnabled, setting.ModelRequestRateLimitEnabled = oldRedis, oldEnabled
	})
	return setting.IndependentRateLimits{Enabled: true, Namespace: uuid.NewString(), Rules: []setting.IndependentRateRule{{ID: "chat", Name: "Non-stream chat", Enabled: true, Path: "/v1/chat/completions", UAMode: "exact", UA: "Go-http-client/2.0", Stream: "non_stream", Limit: 2}}}
}

func storeIndependentTestSettings(t *testing.T, config setting.IndependentRateLimits) {
	t.Helper()
	require.NoError(t, config.Validate())
	raw, err := common.Marshal(config)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[setting.IndependentRateLimitOption] = string(raw)
	common.OptionMapRWMutex.Unlock()
}

func independentTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	router := gin.New()
	router.Use(BodyStorageCleanup(), func(c *gin.Context) { id, _ := strconv.Atoi(c.GetHeader("Test-User")); c.Set("id", id) }, ModelRequestRateLimit())
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/responses/compact"} {
		router.POST(path, func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			c.Data(http.StatusOK, "application/json", body)
		})
	}
	return router
}

func independentTestRequest(router http.Handler, userID int, path, ua, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Test-User", strconv.Itoa(userID))
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

func TestIndependentRateLimitDoesNotThrottleOtherTraffic(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			config := independentTestSettings(t)
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			}
			storeIndependentTestSettings(t, config)
			router := independentTestRouter(t)
			const body = " { \"model\":\"test\", \"stream\":false, \"tools\":[{\"name\":\"lookup\"}], \"metadata\":{\"session_id\":\"original\"}}\n"
			for range 2 {
				w := independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", body)
				require.Equal(t, 200, w.Code)
				require.Equal(t, body, w.Body.String())
			}
			for range 3 {
				w := independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", body)
				require.Equal(t, 429, w.Code)
				require.NotEmpty(t, w.Header().Get("Retry-After"))
				require.Contains(t, w.Body.String(), "independent_request_rate_limit")
			}
			for _, tc := range []struct{ path, ua, body string }{
				{"/v1/responses", "Go-http-client/2.0", `{"stream":false}`},
				{"/v1/responses", "Go-http-client/2.0", `{"stream":true}`},
				{"/v1/responses/compact", "Go-http-client/2.0", `{}`},
				{"/v1/chat/completions", "Go-http-client/2.0", `{"stream":true}`},
				{"/v1/chat/completions", "Other UA", `{"stream":false}`},
				{"/v1/chat/completions", "Go-http-client/2.0-extra", `{"stream":false}`},
				{"/v1/chat/completions", "", `{"stream":false}`},
				{"/v1/chat/completions", "Go-http-client/2.0", `{"stream":false,"stream":true}`},
			} {
				for range 5 {
					w := independentTestRequest(router, 42, tc.path, tc.ua, tc.body)
					require.Equal(t, 200, w.Code, tc)
					require.Equal(t, tc.body, w.Body.String())
				}
			}
			require.Equal(t, 200, independentTestRequest(router, 43, "/v1/chat/completions", "Go-http-client/2.0", body).Code)
			usage, err := requestlimit.Default.Check(context.Background(), config.Namespace, "chat", 42, 2, false)
			require.NoError(t, err)
			require.Equal(t, 2, usage.Count, "unmatched and rejected traffic must not charge the pool")
		})
	}
}

func TestIndependentRateLimitOverridesHotReloadAndMissingStream(t *testing.T) {
	config := independentTestSettings(t)
	config.Rules[0].Limit = 1
	config.Rules[0].Overrides = []setting.UserRateOverride{{UserID: 42, Limit: 2}}
	storeIndependentTestSettings(t, config)
	router := independentTestRouter(t)
	do := func(id int, body string) int {
		return independentTestRequest(router, id, "/v1/chat/completions", "Go-http-client/2.0", body).Code
	}
	require.Equal(t, 200, do(42, `{}`))
	require.Equal(t, 200, do(42, `{"stream":null}`))
	require.Equal(t, 429, do(42, `{"stream":false}`))
	require.Equal(t, 200, do(43, `{}`))
	require.Equal(t, 429, do(43, `{}`))
	config.Rules[0].Overrides[0].Limit = 3
	storeIndependentTestSettings(t, config)
	require.Equal(t, 200, do(42, `{}`))
	require.Equal(t, 429, do(42, `{}`))
	config.Rules[0].Overrides = nil
	storeIndependentTestSettings(t, config)
	require.Equal(t, 429, do(42, `{}`), "editing limits must not reset counts")
	config.Enabled = false
	storeIndependentTestSettings(t, config)
	for range 5 {
		require.Equal(t, 200, do(42, `{}`))
	}
}

func TestIndependentRateLimitLegacyPoolIsolationAndRuleOrder(t *testing.T) {
	config := independentTestSettings(t)
	oldTotal, oldSuccess := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	oldDuration := setting.ModelRequestRateLimitDurationMinutes
	setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes = true, 1, 0, 1
	t.Cleanup(func() {
		setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes = oldTotal, oldSuccess, oldDuration
	})
	second := config.Rules[0]
	second.ID, second.Limit = "second", 100
	config.Rules = append(config.Rules, second)
	storeIndependentTestSettings(t, config)
	router := independentTestRouter(t)
	id := 8900000 + int(modelRateLimitTestUsers.Add(1))
	for range 2 {
		require.Equal(t, 200, independentTestRequest(router, id, "/v1/chat/completions", "Go-http-client/2.0", `{}`).Code)
	}
	require.Equal(t, 429, independentTestRequest(router, id, "/v1/chat/completions", "Go-http-client/2.0", `{}`).Code)
	require.Equal(t, 200, independentTestRequest(router, id, "/v1/responses", "Go-http-client/2.0", `{}`).Code, "independent calls cannot fill the legacy pool")
	require.Equal(t, 429, independentTestRequest(router, id, "/v1/responses", "Go-http-client/2.0", `{}`).Code, "legacy rules still apply to nonmatching traffic")
	usage, err := requestlimit.Default.Check(context.Background(), config.Namespace, second.ID, id, 100, false)
	require.NoError(t, err)
	require.Zero(t, usage.Count, "only the first matching rule is charged")
}

func TestIndependentRateLimitConcurrentAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			config := independentTestSettings(t)
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			}
			config.Rules[0].Limit = 10
			storeIndependentTestSettings(t, config)
			router := independentTestRouter(t)
			var accepted atomic.Int64
			var wg sync.WaitGroup
			for range 100 {
				wg.Go(func() {
					if independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", `{}`).Code == 200 {
						accepted.Add(1)
					}
				})
			}
			wg.Wait()
			require.EqualValues(t, 10, accepted.Load())
		})
	}
}

func TestIndependentRateLimitFailureIsolation(t *testing.T) {
	t.Run("failed upstream requests still count", func(t *testing.T) {
		config := independentTestSettings(t)
		storeIndependentTestSettings(t, config)
		router := gin.New()
		router.Use(BodyStorageCleanup(), func(c *gin.Context) { c.Set("id", 42) }, ModelRequestRateLimit())
		var forwarded int
		router.POST("/v1/chat/completions", func(c *gin.Context) {
			forwarded++
			c.JSON(502, gin.H{"error": "synthetic upstream failure"})
		})
		for range 2 {
			require.Equal(t, 502, independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", `{}`).Code)
		}
		require.Equal(t, 429, independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", `{}`).Code)
		require.Equal(t, 2, forwarded)
	})
	t.Run("unavailable store only affects matched requests", func(t *testing.T) {
		config := independentTestSettings(t)
		storeIndependentTestSettings(t, config)
		oldRedis := common.RDB
		common.RedisEnabled, common.RDB = true, nil
		t.Cleanup(func() { common.RDB = oldRedis })
		router := independentTestRouter(t)
		w := independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", `{}`)
		require.Equal(t, 503, w.Code)
		require.Contains(t, w.Body.String(), "independent_rate_limit_unavailable")
		for _, tc := range []struct{ path, ua, body string }{
			{"/v1/responses", "Go-http-client/2.0", `{"stream":false}`},
			{"/v1/chat/completions", "Go-http-client/2.0", `{"stream":true}`},
			{"/v1/chat/completions", "other", `{"stream":false}`},
		} {
			w = independentTestRequest(router, 42, tc.path, tc.ua, tc.body)
			require.Equal(t, 200, w.Code)
			require.Equal(t, tc.body, w.Body.String())
		}
		config.Enabled = false
		storeIndependentTestSettings(t, config)
		require.Equal(t, 200, independentTestRequest(router, 42, "/v1/chat/completions", "Go-http-client/2.0", `{}`).Code)
	})
}
