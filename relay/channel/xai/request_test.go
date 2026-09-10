package xai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	globalconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeXAIRequestPreservesConversationAndSupportedControls(t *testing.T) {
	const body = `{
		"model":"grok-4.6","instructions":"Keep project_id and paths in user content unchanged.",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},
			{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"offset\":9007199254740993}"},
			{"type":"function_call_output","call_id":"call_1","output":"file content"}
		],
		"tools":[{"type":"function","name":"read","parameters":{"type":"object","properties":{"offset":{"type":"integer"}},"additionalProperties":false},"strict":true}],
		"tool_choice":{"type":"function","name":"read"},"parallel_tool_calls":false,
		"client_metadata":{"session_id":"session-1","installation_id":"device-1"},
		"access_programs":{"cyber":"standard"},"safety_identifier":"private",
		"prompt_cache_key":"stable-cache","prompt_cache_options":{"retention":"24h"},"prompt_cache_retention":"24h",
		"reasoning":{"effort":"max","context":"all_turns","mode":"heavy","summary":"auto"},
		"text":{"verbosity":"high","format":{"type":"json_schema","name":"result","strict":true,"schema":{"type":"object"}}},
		"stream_options":{"include_obfuscation":false,"reasoning_summary_delivery":"sequential_cutoff","include_usage":true},
		"store":false,"stream":true,"include":["reasoning.encrypted_content"],"previous_response_id":"resp_1",
		"service_tier":"priority","temperature":0,"top_p":0,"max_output_tokens":4096,
		"stop":["END"],"presence_penalty":0,"frequency_penalty":0,
		"metadata":{"project_id":"explicit-business-metadata"},"future_extension":{"value":9007199254740993}
	}`
	got, err := normalizeXAIRequestBody([]byte(body))
	require.NoError(t, err)
	for _, name := range []string{"client_metadata", "access_programs", "safety_identifier", "prompt_cache_options", "prompt_cache_retention", "reasoning.context", "reasoning.mode", "text.verbosity", "stream_options.include_obfuscation", "stream_options.reasoning_summary_delivery", "stop", "presence_penalty", "frequency_penalty"} {
		assert.False(t, gjson.GetBytes(got, name).Exists(), name)
	}
	for _, name := range []string{"model", "instructions", "input", "tools", "tool_choice", "parallel_tool_calls", "prompt_cache_key", "reasoning.summary", "text.format", "stream_options.include_usage", "store", "stream", "include", "previous_response_id", "service_tier", "temperature", "top_p", "max_output_tokens", "metadata", "future_extension"} {
		assert.JSONEq(t, gjson.Get(body, name).Raw, gjson.GetBytes(got, name).Raw, name)
	}
	assert.Equal(t, "9007199254740993", gjson.GetBytes(got, "future_extension.value").Raw)
	assert.Equal(t, "xhigh", gjson.GetBytes(got, "reasoning.effort").String())
	assert.Equal(t, "max", gjson.Get(body, "reasoning.effort").String())
	again, err := normalizeXAIRequestBody(got)
	require.NoError(t, err)
	assert.Equal(t, string(got), string(again))
}

func TestNormalizeXAIRequestToolChoiceAndEmptyControls(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"no tools", `{"tool_choice":"auto"}`, `{}`},
		{"empty tools", `{"tools":[],"tool_choice":"auto"}`, `{"tools":[]}`},
		{"null tools", `{"tools":null,"tool_choice":"none"}`, `{"tools":null}`},
		{"required tools", `{"tools":[],"tool_choice":"required"}`, `{"tools":[],"tool_choice":"required"}`},
		{"named tool", `{"tools":[],"tool_choice":{"type":"function","name":"read"}}`, `{"tools":[],"tool_choice":{"type":"function","name":"read"}}`},
		{"real tools", `{"tools":[{"type":"function","name":"read"}],"tool_choice":"auto"}`, `{"tools":[{"type":"function","name":"read"}],"tool_choice":"auto"}`},
		{"malformed tools", `{"tools":{},"tool_choice":"auto"}`, `{"tools":{},"tool_choice":"auto"}`},
		{"empty controls", `{"reasoning":{"context":"all_turns"},"text":{"verbosity":"low"},"stream_options":{"include_obfuscation":false}}`, `{}`},
		{"malformed controls", `{"reasoning":42,"text":null,"stream_options":[]}`, `{"reasoning":42,"text":null,"stream_options":[]}`},
		{"old model", `{"model":"grok-4.3","reasoning":{"effort":"none"},"stop":["END"],"presence_penalty":0}`, `{"model":"grok-4.3","reasoning":{"effort":"none"},"stop":["END"],"presence_penalty":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeXAIRequestBody([]byte(test.body))
			require.NoError(t, err)
			assert.JSONEq(t, test.want, string(got))
		})
	}
	for _, body := range []string{"null", "[]", "broken"} {
		_, err := normalizeXAIRequestBody([]byte(body))
		require.Error(t, err)
	}
}

func TestNormalizeXAIRequestReasoningEffort(t *testing.T) {
	for _, test := range []struct{ model, effort, want string }{
		{"grok-4.6", "none", "low"},
		{"grok-4.6", "minimal", "low"},
		{"grok-4.6", "low", "low"},
		{"grok-4.6", "medium", "medium"},
		{"grok-4.6", "high", "high"},
		{"grok-4.6", "xhigh", "xhigh"},
		{"grok-4.6", "max", "xhigh"},
		{"grok-4.6-beta", "ultra", "xhigh"},
		{"grok-4.20-multi-agent", "max", "xhigh"},
		{"grok-4.5", "xhigh", "high"},
		{"grok-4.5", "max", "high"},
		{"grok-4.3", "none", "none"},
		{"grok-3-mini", "medium", "medium"},
		{"custom-model", "max", "max"},
		{"grok-4.6", "unknown", "unknown"},
	} {
		t.Run(test.model+"/"+test.effort, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":%q,"reasoning":{"effort":%q},"reasoning_effort":%q}`, test.model, test.effort, test.effort)
			got, err := normalizeXAIRequestBody([]byte(body))
			require.NoError(t, err)
			assert.Equal(t, test.want, gjson.GetBytes(got, "reasoning.effort").String())
			assert.Equal(t, test.want, gjson.GetBytes(got, "reasoning_effort").String())
		})
	}
}

func TestXAIRequestCleaningAtTransportBoundary(t *testing.T) {
	t.Setenv("CODEX2API_POLICY_ENABLED", "false")
	for _, test := range []struct {
		path string
		mode int
		body string
	}{
		{"/v1/responses", relayconstant.RelayModeResponses, `{"model":"grok-4.6","input":"hello","tools":[],"tool_choice":"auto","client_metadata":{"session_id":"session-1"},"prompt_cache_key":"cache-1","reasoning":{"effort":"max"},"stop":["END"]}`},
		{"/v1/chat/completions", relayconstant.RelayModeChatCompletions, `{"model":"grok-4.6","messages":[{"role":"user","content":"hello"}],"tools":[],"tool_choice":"auto","client_metadata":{"session_id":"session-1"},"reasoning_effort":"max","stop":["END"]}`},
		{"/v1/images/generations", relayconstant.RelayModeImagesGenerations, `{"model":"grok-imagine-image","prompt":"A mountain","n":1,"response_format":"url"}`},
	} {
		t.Run(test.path, func(t *testing.T) {
			body := test.body
			var captured []byte
			var headers http.Header
			var contentLength int64
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				captured, _ = io.ReadAll(request.Body)
				headers = request.Header.Clone()
				contentLength = request.ContentLength
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(writer, `{"error":{"message":"test capture complete"}}`)
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Request.Header.Set("Session-Id", "session-1")
			info := &relaycommon.RelayInfo{
				RelayMode: test.mode, RequestURLPath: test.path, UpstreamRequestBodySize: int64(len(body)),
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "test-key"},
			}
			response, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(body))
			require.NoError(t, err)
			require.NoError(t, response.(*http.Response).Body.Close())
			assert.Equal(t, int64(len(captured)), contentLength)
			assert.Equal(t, "session-1", headers.Get("Session-Id"))
			assert.Equal(t, "Bearer test-key", headers.Get("Authorization"))
			if test.mode == relayconstant.RelayModeImagesGenerations {
				assert.Equal(t, body, string(captured))
			} else {
				assert.False(t, gjson.GetBytes(captured, "client_metadata").Exists())
				assert.False(t, gjson.GetBytes(captured, "tool_choice").Exists())
				assert.False(t, gjson.GetBytes(captured, "stop").Exists())
				if test.mode == relayconstant.RelayModeResponses {
					assert.Equal(t, "cache-1", gjson.GetBytes(captured, "prompt_cache_key").String())
					assert.Equal(t, "xhigh", gjson.GetBytes(captured, "reasoning.effort").String())
				} else {
					assert.Equal(t, "xhigh", gjson.GetBytes(captured, "reasoning_effort").String())
				}
			}
		})
	}
}

func TestXAIRequestCleaningRejectsOversizedBodyBeforeSending(t *testing.T) {
	previous := globalconstant.MaxRequestBodyMB
	globalconstant.MaxRequestBodyMB = 1
	t.Cleanup(func() { globalconstant.MaxRequestBodyMB = previous })
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	response, err := (&Adaptor{}).DoRequest(ctx, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeResponses}, strings.NewReader(strings.Repeat(" ", (1<<20)+1)))
	assert.Nil(t, response)
	require.ErrorIs(t, err, common.ErrRequestBodyTooLarge)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
}
