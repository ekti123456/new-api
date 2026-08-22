package channel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApplyStableConversationHeadersWithoutParamOverride(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Session-Id", "root-session")
	c.Request.Header.Set("Thread-Id", "child-thread")
	c.Request.Header.Set("X-Codex-Window-Id", "child-thread:2")
	req := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/responses", nil)

	applyStableConversationHeaders(c, req)

	require.Equal(t, "root-session", req.Header.Get("Session-Id"))
	require.Equal(t, "child-thread", req.Header.Get("Thread-Id"))
	require.Equal(t, "child-thread:2", req.Header.Get("X-Codex-Window-Id"))
}
