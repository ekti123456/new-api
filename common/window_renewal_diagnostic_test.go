package common

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestWindowRenewalCorrelationRetainsOriginalRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(RequestIdKey, "original")
	require.Nil(t, WindowRenewalForRequest(c))
	d := BeginWindowRenewalDiagnostic(c, 22)
	d.Renewals = 1
	d.RetryRequestID = "renewed-request"
	d.Result = "retry_sent"
	again := BeginWindowRenewalDiagnostic(c, 22)
	require.Same(t, d, again)
	require.Equal(t, "original", again.FirstRequestID)
	require.Equal(t, "renewed-request", again.RetryRequestID)
	other, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, WindowRenewalForRequest(other), "diagnostics must not cross request scopes")
}
