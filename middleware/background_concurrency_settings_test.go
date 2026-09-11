package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackgroundConcurrencyConfiguredLimitAndPoolIsolation(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			previousRedis, previousClient := common.RedisEnabled, common.RDB
			previousEnabled := setting.ModelRequestConcurrencyLimitEnabled
			previousLimit := setting.GetBackgroundUserConcurrencyLimit()
			common.RedisEnabled = backend == "redis"
			setting.ModelRequestConcurrencyLimitEnabled = true
			resetLocalUserConcurrencyForTest()
			test.Cleanup(func() {
				common.RedisEnabled, common.RDB = previousRedis, previousClient
				setting.ModelRequestConcurrencyLimitEnabled = previousEnabled
				require.NoError(test, setting.UpdateBackgroundUserConcurrencyLimit(strconv.Itoa(previousLimit)))
				resetLocalUserConcurrencyForTest()
			})
			if common.RedisEnabled {
				server := miniredis.RunT(test)
				client := redis.NewClient(&redis.Options{Addr: server.Addr()})
				common.RDB = client
				test.Cleanup(func() { _ = client.Close() })
			}
			for _, limit := range []int{5, 3} {
				require.NoError(test, setting.UpdateBackgroundUserConcurrencyLimit(strconv.Itoa(limit)))
				for index := 0; index < limit-1; index++ {
					acquired, err := acquireUserConcurrency(test.Context(), 42, limit, fmt.Sprintf("held-%d", index), true)
					require.NoError(test, err)
					require.True(test, acquired)
				}
				background, _ := codexUnlinkedNativeTitleContext(42, 7, "01a04915-6f27-7f10-b723-88683446062f")
				router := gin.New()
				router.Use(func(request *gin.Context) {
					request.Set("id", 42)
					common.SetContextKey(request, constant.ContextKeyUserConcurrencyLimit, 1)
					defer common.CleanupBodyStorage(request)
					request.Next()
				})
				router.Use(ModelRequestConcurrencyLimit())
				router.POST("/v1/responses", func(request *gin.Context) { request.Status(http.StatusOK) })
				allowed := httptest.NewRecorder()
				router.ServeHTTP(allowed, background.Request)
				assert.Equal(test, http.StatusOK, allowed.Code, allowed.Body.String())
				current, err := GetUserOccupiedConcurrency(test.Context(), 42)
				require.NoError(test, err)
				assert.Equal(test, limit-1, current, "completed background requests release immediately")
				acquired, err := acquireUserConcurrency(test.Context(), 42, limit, "held-last", true)
				require.NoError(test, err)
				require.True(test, acquired)
				background, _ = codexUnlinkedNativeTitleContext(42, 7, "01a04915-6f27-7f10-b723-88683446062f")
				blocked := httptest.NewRecorder()
				router.ServeHTTP(blocked, background.Request)
				assert.Equal(test, http.StatusTooManyRequests, blocked.Code, blocked.Body.String())
				assert.Contains(test, blocked.Body.String(), fmt.Sprintf("（%d）", limit))
				acquired, err = acquireUserConcurrency(test.Context(), 42, 1, "main-request")
				require.NoError(test, err)
				assert.True(test, acquired, "the full background pool does not use main slots")
				releaseUserConcurrency(42, "main-request", 0)
				releaseUserConcurrency(42, "held-last", 0, true)
				for index := 0; index < limit-1; index++ {
					releaseUserConcurrency(42, fmt.Sprintf("held-%d", index), 0, true)
				}
			}
		})
	}
}
