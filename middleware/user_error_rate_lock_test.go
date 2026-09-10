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
	context.Set(common.RequestIdKey, "lock-response-request")
	context.Header("Retry-After", "120")

	writeUserErrorRateLockResponse(context, perfmetrics.UserErrorRateLockStatus{
		Locked:       true,
		RequestCount: 169,
		ErrorCount:   104,
		ErrorRate:    61.538,
		RetryAfter:   37,
		AccessURL:    "https://chat.example.com",
	})

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, response.Header().Get("Retry-After"))
	assert.Equal(t, "false", response.Header().Get("X-Should-Retry"))
	assert.True(t, context.IsAborted())
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.Contains(t, payload.Error.Message, "最近 169 次请求")
	assert.Contains(t, payload.Error.Message, "61.5%")
	assert.Contains(t, payload.Error.Message, "API 调用已临时暂停")
	assert.Contains(t, payload.Error.Message, "等待 37 秒后手动重试")
	assert.Contains(t, payload.Error.Message, "请勿连续重试")
	assert.Contains(t, payload.Error.Message, "lock-response-request")
	assert.NotContains(t, payload.Error.Message, "检查客户端网络环境")
	assert.Equal(t, "user_error_rate_temporarily_locked", payload.Error.Code)
}
