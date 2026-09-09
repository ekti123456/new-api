package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestRootWaitingConcurrencyLeavesMainSlotsAvailable(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			previousRedis, previousClient := common.RedisEnabled, common.RDB
			common.RedisEnabled = backend == "redis"
			resetLocalUserConcurrencyForTest()
			test.Cleanup(func() {
				common.RedisEnabled, common.RDB = previousRedis, previousClient
				resetLocalUserConcurrencyForTest()
			})
			if common.RedisEnabled {
				server := miniredis.RunT(test)
				client := redis.NewClient(&redis.Options{Addr: server.Addr()})
				common.RDB = client
				test.Cleanup(func() { _ = client.Close() })
			}
			for _, requestID := range []string{"title", "ambient"} {
				acquired, err := acquireUserConcurrency(test.Context(), 42, 2, requestID, true)
				require.NoError(test, err)
				require.True(test, acquired)
			}
			acquired, err := acquireUserConcurrency(test.Context(), 42, 2, "extra-background", true)
			require.NoError(test, err)
			require.False(test, acquired, "waiting requests must remain bounded")
			acquired, err = acquireUserConcurrency(test.Context(), 42, 1, "main")
			require.NoError(test, err)
			require.True(test, acquired, "background requests must not block the main root they are waiting for")
			acquired, err = acquireUserConcurrency(test.Context(), 42, 1, "extra-main")
			require.NoError(test, err)
			require.False(test, acquired, "ordinary concurrency limit must remain unchanged")
			current, err := GetUserCurrentConcurrency(test.Context(), 42)
			require.NoError(test, err)
			require.Equal(test, 3, current)
			occupied, err := GetUserOccupiedConcurrency(test.Context(), 42)
			require.NoError(test, err)
			require.Equal(test, 3, occupied)
			releaseUserConcurrency(42, "title", 0, true)
			releaseUserConcurrency(42, "ambient", 0, true)
			releaseUserConcurrency(42, "main", 0)
			occupied, err = GetUserOccupiedConcurrency(test.Context(), 42)
			require.NoError(test, err)
			require.Zero(test, occupied)
			current, err = GetTotalCurrentConcurrency(test.Context())
			require.NoError(test, err)
			require.Zero(test, current)
		})
	}
}

func TestRootWaitingMiddlewareDoesNotBlockMainRequest(test *testing.T) {
	previousEnabled, previousRedis := setting.ModelRequestConcurrencyLimitEnabled, common.RedisEnabled
	previousCooldown := setting.UserConcurrencyCooldownSeconds
	setting.ModelRequestConcurrencyLimitEnabled, common.RedisEnabled = true, false
	setting.UserConcurrencyCooldownSeconds = 0
	resetLocalUserConcurrencyForTest()
	test.Cleanup(func() {
		setting.ModelRequestConcurrencyLimitEnabled, common.RedisEnabled = previousEnabled, previousRedis
		setting.UserConcurrencyCooldownSeconds = previousCooldown
		resetLocalUserConcurrencyForTest()
	})
	for _, source := range []string{"thread_title", "ambient_suggestions", "agent_created_thread", "memory_consolidation", "guardian_review"} {
		test.Run(source, func(test *testing.T) {
			background, _ := codexUnlinkedNativeTitleContext(42, 7, "01a04915-6f27-7f10-b723-88683446062f")
			if source == "ambient_suggestions" {
				background, _ = codexAmbientSuggestionContext(42, 7)
			} else {
				background.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(background.GetHeader("X-Codex-Turn-Metadata"), "thread_title", source))
			}
			_, backgroundWait := codexUserConcurrencyPolicy(background)
			require.True(test, backgroundWait)
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			done := make(chan struct{})
			router := gin.New()
			router.Use(func(requestContext *gin.Context) {
				requestContext.Set("id", 42)
				common.SetContextKey(requestContext, constant.ContextKeyUserConcurrencyLimit, 1)
			})
			router.Use(ModelRequestConcurrencyLimit())
			router.POST("/v1/responses", func(requestContext *gin.Context) {
				if requestContext.GetHeader("X-Codex-Turn-Metadata") != "" {
					close(entered)
					<-release
				}
				requestContext.Status(http.StatusOK)
			})
			backgroundRecorder := httptest.NewRecorder()
			go func() {
				router.ServeHTTP(backgroundRecorder, background.Request)
				close(done)
			}()
			select {
			case <-entered:
			case <-done:
				test.Fatalf("background admission failed: %d %s", backgroundRecorder.Code, backgroundRecorder.Body.String())
			}
			mainRecorder := httptest.NewRecorder()
			mainRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","input":"main task"}`))
			mainRequest.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(mainRecorder, mainRequest)
			require.Equal(test, http.StatusOK, mainRecorder.Code, mainRecorder.Body.String())
			test.Cleanup(func() { <-done })
		})
	}
	invalid, _ := gin.CreateTestContext(httptest.NewRecorder())
	invalid.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol"}`))
	invalid.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_source":"thread_title"}`)
	_, backgroundWait := codexUserConcurrencyPolicy(invalid)
	require.False(test, backgroundWait, "a source label alone cannot obtain a protected slot")
}
