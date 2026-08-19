package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelRPMRateLimitOnlyRestrictsConfiguredModelPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, _ = useRateLimitMiniRedis(t)
	previousEnabled := setting.ModelRPMRateLimitEnabled
	previousLimits := setting.ModelRPMRateLimitModels
	setting.ModelRPMRateLimitEnabled = true
	setting.ModelRPMRateLimitModels = map[string]int{"gpt-limited": 1}
	t.Cleanup(func() {
		setting.ModelRPMRateLimitEnabled = previousEnabled
		setting.ModelRPMRateLimitModels = previousLimits
	})

	request := func(userID int, modelName string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		ctx.Set("id", userID)
		if enforceModelRPMRateLimit(ctx, modelName) {
			ctx.Status(http.StatusNoContent)
		}
		ctx.Writer.WriteHeaderNow()
		return response
	}

	require.Equal(t, http.StatusNoContent, request(101, "gpt-limited").Code)
	limited := request(101, "gpt-limited")
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.NotEmpty(t, limited.Header().Get("Retry-After"))
	require.Equal(t, http.StatusNoContent, request(101, "gpt-unlisted").Code)
	require.Equal(t, http.StatusNoContent, request(102, "gpt-limited").Code)
	require.True(t, common.RedisEnabled)
}
