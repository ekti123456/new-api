package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionRejectsInvalidUserTtftExcessThreshold(t *testing.T) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/",
		strings.NewReader(`{"key":"perf_metrics_setting.user_ttft_over_average_percent","value":"1001"}`),
	)

	UpdateOption(context)

	assert.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.Contains(t, payload.Message, "1000")
}

func TestUpdateOptionRejectsInvalidUserErrorRateLockValues(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		match string
	}{
		{name: "minimum samples", body: `{"key":"perf_metrics_setting.user_error_rate_lock_min_requests","value":"0"}`, match: "100000"},
		{name: "error rate", body: `{"key":"perf_metrics_setting.user_error_rate_lock_threshold","value":"101"}`, match: "100"},
		{name: "lock duration", body: `{"key":"perf_metrics_setting.user_error_rate_lock_seconds","value":"86401"}`, match: "86400"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(test.body))

			UpdateOption(context)

			assert.Equal(t, http.StatusOK, response.Code)
			var payload struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			assert.False(t, payload.Success)
			assert.Contains(t, payload.Message, test.match)
		})
	}
}
