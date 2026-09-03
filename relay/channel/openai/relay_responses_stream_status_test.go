package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type responsesWriteFailureWriter struct {
	gin.ResponseWriter
}

func (w *responsesWriteFailureWriter) WriteString(string) (int, error) {
	return 0, errors.New("downstream write failed")
}

func TestOaiResponsesStreamHandlerTerminalEventStopsOpenUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	for _, terminalType := range []string{"response.completed", "response.done"} {
		t.Run(terminalType, func(t *testing.T) {
			pr, pw := io.Pipe()
			t.Cleanup(func() {
				_ = pr.Close()
				_ = pw.Close()
			})

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set(common.RequestIdKey, "responses-stream-terminal-test")
			info := &relaycommon.RelayInfo{
				OriginModelName: "gpt-5.6-sol",
				DisablePing:     true,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "gpt-5.6-sol",
				},
			}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       pr,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			}

			var promptTokens, completionTokens, totalTokens, cachedTokens int
			var apiErrPresent bool
			done := make(chan struct{})
			go func() {
				usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
				apiErrPresent = apiErr != nil
				if usage != nil {
					promptTokens = usage.PromptTokens
					completionTokens = usage.CompletionTokens
					totalTokens = usage.TotalTokens
					cachedTokens = usage.PromptTokensDetails.CachedTokens
				}
				close(done)
			}()

			body := strings.Join([]string{
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_1","status":"completed"}}`,
				``,
				`data: {"type":"` + terminalType + `","response":{"status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":5}}}}`,
				``,
			}, "\n")
			_, err := io.WriteString(pw, body)
			require.NoError(t, err)

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = pr.Close()
				t.Fatal("handler waited for [DONE]/EOF after a terminal Responses event")
			}

			assert.False(t, apiErrPresent)
			require.NotNil(t, info.StreamStatus)
			assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
			assert.False(t, info.StreamStatus.HasErrors())
			assert.Equal(t, 11, promptTokens)
			assert.Equal(t, 7, completionTokens)
			assert.Equal(t, 18, totalTokens)
			assert.Equal(t, 5, cachedTokens)
			require.NotNil(t, info.ResponsesUsageInfo)
			require.Contains(t, info.ResponsesUsageInfo.BuiltInTools, "web_search_preview")
			assert.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools["web_search_preview"].CallCount)
			assert.Contains(t, w.Body.String(), `"type":"`+terminalType+`"`)
		})
	}
}

func TestOaiResponsesStreamHandlerTerminalWriteFailureIsRecorded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Writer = &responsesWriteFailureWriter{ResponseWriter: c.Writer}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		DisablePing:     true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.6-sol",
		},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	var promptTokens, completionTokens int
	done := make(chan struct{})
	go func() {
		usage, _ := OaiResponsesStreamHandler(c, info, resp)
		if usage != nil {
			promptTokens = usage.PromptTokens
			completionTokens = usage.CompletionTokens
		}
		close(done)
	}()

	_, err := io.WriteString(pw, `data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`+"\n\n")
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = pr.Close()
		t.Fatal("handler did not stop after downstream write failure")
	}

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.Contains(t, info.StreamStatus.Summary(), "downstream write failed")
	assert.Equal(t, 3, promptTokens, "terminal usage must be retained before the write attempt")
	assert.Equal(t, 2, completionTokens, "terminal usage must be retained before the write attempt")
}

func TestOaiResponsesStreamHandlerMarksResponseFailedAsStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
	})

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"status":"in_progress"}}`,
		"",
		`data: {"type":"response.failed","response":{"status":"failed","error":{"type":"upstream_error","code":"upstream_stream_break","message":"Upstream stream ended prematurely; safe to retry"}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "responses-stream-failed-test")
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		DisablePing:     true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.6-sol",
		},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	_, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, info.StreamStatus)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.Contains(t, info.StreamStatus.Summary(), "soft_errors=1")
	assert.Contains(t, w.Body.String(), `"type":"response.failed"`)
}
