package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteUserErrorRateLockResponse(t *testing.T) {
	consoleSetting := console_setting.GetConsoleSetting()
	originalConsoleSetting := *consoleSetting
	t.Cleanup(func() { *consoleSetting = originalConsoleSetting })
	consoleSetting.ApiInfo = `[
		{"url":"https://chat.example.com/","route":"primary","description":"Recommended route","color":"blue"},
		{"url":"https://cf-chat.example.com","route":"cdn","description":"CDN route","color":"green"}
	]`
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
		AccessURL:    "https://chat.example.com",
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
	assert.Contains(t, payload.Error.Message, "本次访问 URL：https://chat.example.com")
	assert.Contains(t, payload.Error.Message, "URL：https://chat.example.com/")
	assert.Contains(t, payload.Error.Message, "说明信息：Recommended route")
	assert.Contains(t, payload.Error.Message, "URL：https://cf-chat.example.com")
	assert.Contains(t, payload.Error.Message, "说明信息：CDN route")
	assert.Equal(t, "user_error_rate_temporarily_locked", payload.Error.Code)
}
