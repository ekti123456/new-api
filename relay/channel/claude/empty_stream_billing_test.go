package claude

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeEmptyStreamDoesNotConvertReservationEstimateToUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, tc := range []struct {
			name, body      string
			cancel, timeout bool
		}{
			{name: "client cancellation before first event", cancel: true},
			{name: "timeout before first event", timeout: true},
			{name: "empty EOF"},
			{name: "heartbeat only", body: "data: {\"type\":\"ping\"}\n\n"},
			{name: "empty text block", body: "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"},
		} {
			t.Run(string(format)+"/"+tc.name, func(t *testing.T) {
				requestCtx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)
				info := &relaycommon.RelayInfo{
					RelayFormat: format, IsStream: true, DisablePing: true,
					ShouldIncludeUsage: true,
					ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-5-5"},
				}
				info.SetEstimatePromptTokens(2014151)
				var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
				if tc.cancel || tc.timeout {
					reader, writer := io.Pipe()
					t.Cleanup(func() { _ = writer.Close() })
					body = reader
					if tc.cancel {
						cancel()
					} else {
						constant.StreamingTimeout = 1
						t.Cleanup(func() { constant.StreamingTimeout = 30 })
					}
				}
				usage, apiErr := ClaudeStreamHandler(ctx, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, info)
				require.Nil(t, apiErr, "finish accounting without starting another upstream attempt")
				require.NotNil(t, usage, "nil usage would reintroduce the settlement estimate fallback")
				assert.Zero(t, usage.PromptTokens)
				assert.Zero(t, usage.CompletionTokens)
				assert.Nil(t, usage.BillingUsage)
				assert.True(t, info.EmptyUpstreamResponse)
				assert.False(t, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens))
				require.NotNil(t, info.StreamStatus)
				assert.Equal(t, "none", info.StreamStatus.DeliverySnapshot().UsageSource)
				if tc.cancel {
					assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
				} else if tc.timeout {
					assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
				} else {
					assert.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
					assert.True(t, info.StreamStatus.HasErrors(), "empty EOF must remain visible as a failed stream")
				}
			})
		}
	}
}

func TestClaudePartialUsageEstimatesRemainConsistent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	text := "The partial response contains several words before disconnecting."
	delta := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + text + `"}}`
	start := `{"type":"message_start","message":{"usage":{"input_tokens":0,"output_tokens":1,"cache_read_input_tokens":5000,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":100}}}}`
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, tc := range []struct {
			name                           string
			events                         []string
			input, output, cache, creation int
			estimated                      bool
		}{
			{"text without usage", []string{delta}, 123, service.EstimateTokenByModel("claude-opus-5-5", text), 0, 0, true},
			{"text at block start", []string{`{"type":"content_block_start","content_block":{"type":"text","text":"` + text + `"}}`}, 123, service.EstimateTokenByModel("claude-opus-5-5", text), 0, 0, true},
			{"cached input and partial output", []string{start, delta}, 0, service.EstimateTokenByModel("claude-opus-5-5", text), 5000, 100, true},
			{"stop without final usage", []string{start, delta, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`}, 0, service.EstimateTokenByModel("claude-opus-5-5", text), 5000, 100, true},
			{"complete measured output", []string{start, delta, `{"type":"message_delta","usage":{"output_tokens":2}}`}, 0, 2, 5000, 100, false},
			{"measured zero output", []string{start, delta, `{"type":"message_delta","usage":{"output_tokens":0}}`}, 0, 0, 5000, 100, false},
			{"measured zero input before partial output", []string{`{"type":"message_start","message":{"usage":{"input_tokens":0,"output_tokens":0}}}`, delta}, 0, service.EstimateTokenByModel("claude-opus-5-5", text), 0, 0, true},
		} {
			t.Run(string(format)+"/"+tc.name, func(t *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				info := &relaycommon.RelayInfo{RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-5-5"}}
				info.SetEstimatePromptTokens(123)
				state := &ClaudeResponseInfo{Usage: &dto.Usage{}}
				for _, event := range tc.events {
					require.Nil(t, HandleStreamResponseData(ctx, info, state, event))
				}
				HandleStreamFinalResponse(ctx, info, state)
				assert.False(t, info.EmptyUpstreamResponse)
				assert.Equal(t, tc.input, state.Usage.PromptTokens)
				assert.Equal(t, tc.output, state.Usage.CompletionTokens)
				require.NotNil(t, state.Usage.BillingUsage)
				assert.Equal(t, tc.estimated, state.Usage.BillingUsage.Estimated)
				assert.Equal(t, tc.estimated, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens))
				billed := state.Usage.BillingUsage.ClaudeUsage
				require.NotNil(t, billed)
				assert.Equal(t, tc.input, billed.InputTokens)
				assert.Equal(t, tc.output, billed.OutputTokens, "the structured billing snapshot must include the estimated output")
				assert.Equal(t, tc.cache, billed.CacheReadInputTokens)
				assert.Equal(t, tc.creation, billed.CacheCreationInputTokens)
				assert.Equal(t, tc.creation, billed.GetCacheCreation5mTokens())
			})
		}
	}
}

func TestClaudeStreamPreservesBillableEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, event string
		wantEmpty   bool
	}{
		{"measured input without output", `{"type":"message_start","message":{"usage":{"input_tokens":1000,"output_tokens":0}}}`, false},
		{"fully cached input without output", `{"type":"message_start","message":{"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":5000}}}`, false},
		{"text delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`, false},
		{"text at block start", `{"type":"content_block_start","content_block":{"type":"text","text":"hello"}}`, false},
		{"thinking delta", `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reasoning"}}`, false},
		{"opaque reasoning", `{"type":"content_block_start","content_block":{"type":"redacted_thinking","data":"opaque"}}`, false},
		{"tool call without text", `{"type":"content_block_start","content_block":{"type":"tool_use","id":"tool_1","name":"read_file","input":{}}}`, false},
		{"server tool call", `{"type":"content_block_start","content_block":{"type":"server_tool_use","id":"tool_2","name":"web_search","input":{}}}`, false},
		{"tool argument delta", `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}`, false},
		{"tool usage without token usage", `{"type":"message_delta","usage":{"server_tool_use":{"web_search_requests":1}}}`, false},
		{"explicit zero usage", `{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":0}}`, true},
		{"empty thinking placeholder", `{"type":"content_block_start","content_block":{"type":"thinking","thinking":""}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-5-5"}}
			info.SetEstimatePromptTokens(2014151)
			state := &ClaudeResponseInfo{Usage: &dto.Usage{}}
			require.Nil(t, HandleStreamResponseData(ctx, info, state, tc.event))
			measuredUsage := dto.CloneBillingUsage(state.Usage.BillingUsage)
			HandleStreamFinalResponse(ctx, info, state)
			assert.Equal(t, tc.wantEmpty, info.EmptyUpstreamResponse)
			if measuredUsage != nil {
				assert.Equal(t, measuredUsage, state.Usage.BillingUsage, "measured input/cache must still be settled after a disconnect")
			}
			if tc.wantEmpty {
				assert.Zero(t, state.Usage.PromptTokens)
				assert.Zero(t, state.Usage.CompletionTokens)
			}
		})
	}
}
