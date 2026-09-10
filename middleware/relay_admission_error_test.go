package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAdmissionErrorDatabase(test *testing.T) *gorm.DB {
	test.Helper()
	require.NoError(test, perfmetrics.FlushAdmissionErrors(test.Context()))
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(test, err)
	sqlDB, err := database.DB()
	require.NoError(test, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(test, database.AutoMigrate(&model.PerfMetricError{}, &model.Log{}, &model.PerfMetric{}))
	originalDB, originalLogDB := model.DB, model.LOG_DB
	performanceSettings, ok := config.GlobalConfig.Get("perf_metrics_setting").(*perf_metrics_setting.PerfMetricsSetting)
	require.True(test, ok)
	originalErrorLog, originalPerformance := constant.ErrorLogEnabled, performanceSettings.Enabled
	model.DB, model.LOG_DB = database, database
	constant.ErrorLogEnabled, performanceSettings.Enabled = true, true
	test.Cleanup(func() {
		flushContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(test, perfmetrics.FlushAdmissionErrors(flushContext))
		model.DB, model.LOG_DB = originalDB, originalLogDB
		constant.ErrorLogEnabled, performanceSettings.Enabled = originalErrorLog, originalPerformance
		require.NoError(test, sqlDB.Close())
	})
	return database
}

func TestBackgroundConcurrencyRejectionIsAuditedBeforeDispatch(test *testing.T) {
	database := setupAdmissionErrorDatabase(test)
	originalEnabled, originalRedis := setting.ModelRequestConcurrencyLimitEnabled, common.RedisEnabled
	setting.ModelRequestConcurrencyLimitEnabled, common.RedisEnabled = true, false
	resetLocalUserConcurrencyForTest()
	test.Cleanup(func() {
		setting.ModelRequestConcurrencyLimitEnabled, common.RedisEnabled = originalEnabled, originalRedis
		resetLocalUserConcurrencyForTest()
	})
	for _, requestID := range []string{"background-one", "background-two"} {
		acquired, err := acquireUserConcurrency(test.Context(), 42, 2, requestID, true)
		require.NoError(test, err)
		require.True(test, acquired)
	}
	background, _ := codexUnlinkedNativeTitleContext(42, 7, "01a04915-6f27-7f10-b723-88683446062f")
	router := gin.New()
	router.Use(RequestId(), RouteTag("relay"))
	router.Use(func(requestContext *gin.Context) {
		requestContext.Set("id", 42)
		requestContext.Set("username", "background-user")
		requestContext.Set("token_id", 7)
		common.SetContextKey(requestContext, constant.ContextKeyUsingGroup, "gpt-pro")
		common.SetContextKey(requestContext, constant.ContextKeyUserConcurrencyLimit, 1)
		defer common.CleanupBodyStorage(requestContext)
		requestContext.Next()
	})
	router.Use(ModelRequestConcurrencyLimit())
	upstreamCalls := 0
	router.POST("/v1/responses", func(requestContext *gin.Context) {
		upstreamCalls++
		requestContext.Status(http.StatusOK)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, background.Request)
	require.Equal(test, http.StatusTooManyRequests, response.Code, response.Body.String())
	assert.Zero(test, upstreamCalls)
	assert.Equal(test, "1", response.Header().Get("Retry-After"))
	require.NoError(test, perfmetrics.FlushAdmissionErrors(test.Context()))
	var failures []model.PerfMetricError
	require.NoError(test, database.Find(&failures).Error)
	require.Len(test, failures, 1)
	assert.Equal(test, response.Header().Get(common.RequestIdKey), failures[0].RequestId)
	assert.Equal(test, "user_concurrency_limit_exceeded", failures[0].ErrorCode)
	assert.Equal(test, "new_api_admission_error", failures[0].ErrorType)
	assert.Equal(test, http.StatusTooManyRequests, failures[0].StatusCode)
	assert.Equal(test, 42, failures[0].UserId)
	assert.Equal(test, "gpt-pro", failures[0].Group)
	assert.NotEmpty(test, failures[0].ModelName)
	assert.Zero(test, failures[0].ChannelId)
	assert.Contains(test, failures[0].ErrorReason, `"thread_source":"thread_title"`)
	assert.Contains(test, failures[0].ErrorReason, `"stage":"relay_admission"`)
	var usage []model.Log
	require.NoError(test, database.Find(&usage).Error)
	require.Len(test, usage, 1)
	assert.Equal(test, model.LogTypeError, usage[0].Type)
	assert.Equal(test, failures[0].RequestId, usage[0].RequestId)
	assert.Zero(test, usage[0].Quota)
	assert.Zero(test, usage[0].ChannelId)
	var samples int64
	require.NoError(test, database.Model(&model.PerfMetric{}).Count(&samples).Error)
	assert.Zero(test, samples, "admission rejection must not become an upstream performance failure")
}

type unreadAdmissionBody struct{}

func (unreadAdmissionBody) Read([]byte) (int, error) {
	panic("admission audit must not read an unparsed request body")
}

func TestLockedAdmissionAuditsHTTPAndWebSocketWithoutReadingBody(test *testing.T) {
	database := setupAdmissionErrorDatabase(test)
	constant.ErrorLogEnabled = false
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		test.Run(method, func(test *testing.T) {
			response := httptest.NewRecorder()
			requestContext, _ := gin.CreateTestContext(response)
			requestContext.Request = httptest.NewRequest(method, "/v1/responses?secret=not-for-logs", unreadAdmissionBody{})
			requestContext.Request.Header.Set("Authorization", "Bearer not-for-logs")
			requestContext.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_source":"subagent","subagent_kind":"thread_spawn","request_kind":"turn","installation_id":"not-for-logs"}`)
			if method == http.MethodGet {
				requestContext.Request.Header.Set("Upgrade", "websocket")
				requestContext.Request.Header.Set("Connection", "Upgrade")
			}
			requestContext.Set("id", 83)
			requestContext.Set(RouteTagKey, "relay")
			requestContext.Set(common.RequestIdKey, "locked-"+method)
			writeUserErrorRateLockResponse(requestContext, perfmetrics.UserErrorRateLockStatus{Locked: true, RetryAfter: 120})
			assert.Equal(test, http.StatusBadRequest, response.Code)
			assert.Equal(test, "false", response.Header().Get("X-Should-Retry"))
			assert.Empty(test, response.Header().Get("Retry-After"))
			recordRelayAdmissionError(requestContext, http.StatusBadRequest, "user_error_rate_temporarily_locked", "duplicate")
		})
	}
	require.NoError(test, perfmetrics.FlushAdmissionErrors(test.Context()))
	var failures []model.PerfMetricError
	require.NoError(test, database.Find(&failures).Error)
	require.Len(test, failures, 2, "one audit row per rejected request, not per callback")
	for _, failure := range failures {
		assert.Empty(test, failure.ModelName, "do not invent a model before the request is parsed")
		assert.Contains(test, failure.ErrorReason, `"subagent_kind":"thread_spawn"`)
		assert.NotContains(test, failure.ErrorReason, "not-for-logs")
		assert.Equal(test, "user_error_rate_temporarily_locked", failure.ErrorCode)
	}
	var userLogs int64
	require.NoError(test, database.Model(&model.Log{}).Count(&userLogs).Error)
	assert.Zero(test, userLogs, "admin audit must work even with ERROR_LOG_ENABLED disabled")
}

func TestAdmissionAuditHonorsRouteAuthenticationAndSettings(test *testing.T) {
	database := setupAdmissionErrorDatabase(test)
	for _, scenario := range []struct {
		name        string
		userID      int
		route       string
		performance bool
		errorLog    bool
	}{
		{name: "anonymous", route: "relay", performance: true, errorLog: true},
		{name: "dashboard", userID: 42, route: "web", performance: true, errorLog: true},
		{name: "disabled", userID: 42, route: "relay"},
		{name: "error-log-only", userID: 42, route: "relay", errorLog: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			performanceSettings, ok := config.GlobalConfig.Get("perf_metrics_setting").(*perf_metrics_setting.PerfMetricsSetting)
			require.True(test, ok)
			performanceSettings.Enabled, constant.ErrorLogEnabled = scenario.performance, scenario.errorLog
			requestContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			requestContext.Set("id", scenario.userID)
			requestContext.Set(RouteTagKey, scenario.route)
			requestContext.Set(common.RequestIdKey, scenario.name)
			abortWithOpenAiMessage(requestContext, http.StatusTooManyRequests, "当前并发请求已达到上限", types.ErrorCode("user_concurrency_limit_exceeded"))
			require.NoError(test, perfmetrics.FlushAdmissionErrors(test.Context()))
		})
	}
	var failures int64
	require.NoError(test, database.Model(&model.PerfMetricError{}).Count(&failures).Error)
	assert.Zero(test, failures)
	var userLogs []model.Log
	require.NoError(test, database.Find(&userLogs).Error)
	require.Len(test, userLogs, 1)
	assert.Equal(test, "error-log-only", userLogs[0].RequestId)
}

func TestRequestRateLimitRejectsHaveErrorCodesForBothBackends(test *testing.T) {
	_, _ = useRateLimitMiniRedis(test)
	for _, backend := range []string{"memory", "redis"} {
		for _, limit := range []struct {
			name    string
			total   int
			success int
			code    string
		}{
			{name: "total", total: 1, success: 100, code: "user_total_request_rate_limit_exceeded"},
			{name: "success", total: 100, success: 1, code: "user_request_rate_limit_exceeded"},
		} {
			test.Run(backend+"/"+limit.name, func(test *testing.T) {
				var limiter gin.HandlerFunc
				if backend == "redis" {
					limiter = redisRateLimitHandler(60, limit.total, limit.success)
				} else {
					limiter = memoryRateLimitHandler(60, limit.total, limit.success)
				}
				router := gin.New()
				router.Use(func(requestContext *gin.Context) { requestContext.Set("id", 900+limit.total) })
				router.Use(limiter)
				calls := 0
				router.POST("/v1/responses", func(requestContext *gin.Context) { calls++; requestContext.Status(http.StatusOK) })
				for attempt := 0; attempt < 2; attempt++ {
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
					if attempt == 0 {
						require.Equal(test, http.StatusOK, response.Code)
					} else {
						require.Equal(test, http.StatusTooManyRequests, response.Code)
						assert.Contains(test, response.Body.String(), fmt.Sprintf(`"code":"%s"`, limit.code))
					}
				}
				assert.Equal(test, 1, calls)
			})
		}
	}
}
