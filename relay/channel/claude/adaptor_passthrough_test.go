package claude

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClaudeHeaderTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-code/test")
	c.Request.Header.Set("X-Stainless-OS", "windows")
	c.Request.Header.Set("x-stainless-helper-method", "stream")
	c.Request.Header.Set("x-claude-code-session-id", "session-1")
	c.Request.Header.Set("x-client-request-id", "request-1")
	c.Request.Header.Set("anthropic-beta", "context-1")
	c.Request.Header.Set("authorization", "Bearer client-credential")
	c.Request.Header.Set("x-api-key", "client-key")
	return c
}

func TestSetupRequestHeaderClaudePassthroughPreservesClientHeadersAndReplacesAuth(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPassThrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = true
	t.Cleanup(func() { settings.PassThroughRequestEnabled = originalPassThrough })

	c := newClaudeHeaderTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-sonnet",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "channel-key"},
	}
	requestHeaders := make(http.Header)
	err := (&Adaptor{}).SetupRequestHeader(c, &requestHeaders, info)
	require.NoError(t, err)

	assert.Equal(t, "claude-code/test", requestHeaders.Get("User-Agent"))
	assert.Equal(t, "windows", requestHeaders.Get("X-Stainless-OS"))
	assert.Equal(t, "stream", requestHeaders.Get("x-stainless-helper-method"))
	assert.Equal(t, "session-1", requestHeaders.Get("x-claude-code-session-id"))
	assert.Equal(t, "request-1", requestHeaders.Get("x-client-request-id"))
	assert.Equal(t, "context-1", requestHeaders.Get("anthropic-beta"))
	assert.Equal(t, "channel-key", requestHeaders.Get("x-api-key"))
	assert.Empty(t, requestHeaders.Get("authorization"))
}

func TestSetupRequestHeaderClaudeWithoutPassthroughDoesNotCopyClientRuntimeHeaders(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	originalPassThrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() { settings.PassThroughRequestEnabled = originalPassThrough })

	c := newClaudeHeaderTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "channel-key"},
	}
	requestHeaders := make(http.Header)
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &requestHeaders, info))

	assert.Empty(t, requestHeaders.Get("User-Agent"))
	assert.Empty(t, requestHeaders.Get("X-Stainless-OS"))
	assert.Empty(t, requestHeaders.Get("x-claude-code-session-id"))
	assert.Equal(t, "channel-key", requestHeaders.Get("x-api-key"))
	assert.Empty(t, requestHeaders.Get("authorization"))
}
