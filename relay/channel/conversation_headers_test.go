package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type stableConversationWebSocketAdaptor struct {
	Adaptor
	url string
}

func (a stableConversationWebSocketAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.url, nil
}

func (stableConversationWebSocketAdaptor) SetupRequestHeader(_ *gin.Context, _ *http.Header, _ *relaycommon.RelayInfo) error {
	return nil
}

func TestApplyStableConversationHeadersWithoutParamOverride(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Session-Id", "root-session")
	c.Request.Header.Set("X-Session-ID", "generic-session")
	c.Request.Header.Set("OpenAI-Session-ID", "openai-session")
	c.Request.Header.Set("Thread-Id", "child-thread")
	c.Request.Header.Set("X-Codex-Window-Id", "child-thread:2")
	c.Request.Header.Set("X-OpenAI-Subagent", "guardian")
	req := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/responses", nil)

	applyStableConversationHeaders(c, req)

	require.Equal(t, "root-session", req.Header.Get("Session-Id"))
	require.Equal(t, "generic-session", req.Header.Get("X-Session-ID"))
	require.Equal(t, "openai-session", req.Header.Get("OpenAI-Session-ID"))
	require.Equal(t, "child-thread", req.Header.Get("Thread-Id"))
	require.Equal(t, "child-thread:2", req.Header.Get("X-Codex-Window-Id"))
	require.Equal(t, "guardian", req.Header.Get("X-OpenAI-Subagent"))
}

func TestDoWssRequestForwardsStableConversationHeaders(t *testing.T) {
	t.Setenv("CODEX2API_POLICY_ENABLED", "false")
	received := make(chan http.Header, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(server.Close)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("Session-Id", "root-session")
	c.Request.Header.Set("X-Session-ID", "generic-session")
	c.Request.Header.Set("OpenAI-Session-ID", "openai-session")
	c.Request.Header.Set("Thread-Id", "child-thread")
	c.Request.Header.Set("X-Codex-Window-Id", "child-thread:3")
	c.Request.Header.Set("X-OpenAI-Subagent", "guardian")

	adaptor := stableConversationWebSocketAdaptor{url: "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"}
	conn, err := DoWssRequest(adaptor, c, &relaycommon.RelayInfo{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	headers := <-received
	require.Equal(t, "root-session", headers.Get("Session-Id"))
	require.Equal(t, "generic-session", headers.Get("X-Session-ID"))
	require.Equal(t, "openai-session", headers.Get("OpenAI-Session-ID"))
	require.Equal(t, "child-thread", headers.Get("Thread-Id"))
	require.Equal(t, "child-thread:3", headers.Get("X-Codex-Window-Id"))
	require.Equal(t, "guardian", headers.Get("X-OpenAI-Subagent"))
}
