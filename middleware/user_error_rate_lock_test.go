package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteUserErrorRateLockResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	writeUserErrorRateLockResponse(context, perfmetrics.UserErrorRateLockStatus{
		Locked:       true,
		RequestCount: 169,
		ErrorCount:   104,
		ErrorRate:    61.538,
		RetryAfter:   37,
	})

	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "37", response.Header().Get("Retry-After"))
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.Contains(t, payload.Error.Message, "最近 169 次请求")
	assert.Contains(t, payload.Error.Message, "61.5%")
	assert.Contains(t, payload.Error.Message, "检查客户端网络环境")
	assert.Equal(t, "user_error_rate_temporarily_locked", payload.Error.Code)
}
