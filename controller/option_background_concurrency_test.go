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

func TestUpdateOptionRejectsInvalidBackgroundConcurrency(test *testing.T) {
	for _, value := range []string{"0", "-1", "100001", "2.5", "invalid", ""} {
		test.Run(value, func(test *testing.T) {
			response := httptest.NewRecorder()
			request, _ := gin.CreateTestContext(response)
			request.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"BackgroundUserConcurrencyLimit","value":"`+value+`"}`))
			UpdateOption(request)
			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(test, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(test, result.Success)
			assert.Contains(test, result.Message, "between 1 and 100000")
		})
	}
}
