package common

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamResponseModelEnvelopesAndIsolation(t *testing.T) {
	previous := UpstreamResponseModelLogEnabled.Load()
	t.Cleanup(func() { UpstreamResponseModelLogEnabled.Store(previous) })
	UpstreamResponseModelLogEnabled.Store(true)
	for _, tc := range []struct{ name, payload, want string }{
		{"responses", `{"type":"response.created","response":{"model":"gpt-5.6-sol"}}`, "gpt-5.6-sol"},
		{"chat", `{"model":"gpt-5.6-luna","choices":[]}`, "gpt-5.6-luna"},
		{"claude", `{"type":"message_start","message":{"model":"claude-sonnet-4-6"}}`, "claude-sonnet-4-6"},
		{"gemini", `{"modelVersion":"gemini-2.5-pro"}`, "gemini-2.5-pro"},
		{"realtime", `{"type":"session.created","session":{"model":"gpt-realtime"}}`, "gpt-realtime"},
		{"business content", `{"type":"response.output_text.delta","delta":"{\"model\":\"not-real\"}","metadata":{"model":"also-not-real"}}`, ""},
		{"missing", `{"output":[]}`, ""},
		{"invalid JSON", `{"model":"gpt-6-astra",`, ""},
		{"non-string", `{"model":123}`, ""},
		{"credential", `{"model":"sk-sensitive"}`, ""},
		{"multiline", `{"model":"gpt-6\nsecret"}`, ""},
		{"oversized name", `{"model":"` + strings.Repeat("x", 201) + `"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			BeginUpstreamResponseModel(c).Observe([]byte(tc.payload), "")
			name, conflict := GetUpstreamResponseModel(c)
			assert.Equal(t, tc.want, name)
			assert.False(t, conflict)
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	observation := BeginUpstreamResponseModel(c)
	observation.Observe([]byte(`{"response":{"model":"first"}}`), "response.created")
	observation.Observe([]byte(`{"response":{"model":"final"}}`), "response.completed")
	name, conflict := GetUpstreamResponseModel(c)
	assert.Equal(t, "final", name)
	assert.True(t, conflict)
	BeginUpstreamResponseModel(c)
	name, conflict = GetUpstreamResponseModel(c)
	assert.Empty(t, name, "a retry cannot inherit the previous attempt's response model")
	assert.False(t, conflict)
	UpstreamResponseModelLogEnabled.Store(false)
	require.Nil(t, BeginUpstreamResponseModel(c))
	observation.Observe([]byte(`{"model":"disabled"}`), "response.completed")
	name, _ = GetUpstreamResponseModel(c)
	assert.Empty(t, name)
}
