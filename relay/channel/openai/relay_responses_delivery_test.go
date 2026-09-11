package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type responsesDeliveryBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func TestResponsesTerminalUsageSourceAndExplicitZero(test *testing.T) {
	service.InitTokenEncoders()
	gin.SetMode(gin.TestMode)
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	test.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	for _, scenario := range []struct {
		name       string
		usageJSON  string
		wantSource string
		wantTotal  int
	}{
		{name: "explicit zero is authoritative", usageJSON: `{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, wantSource: "upstream", wantTotal: 0},
		{name: "incomplete usage is retained", usageJSON: `{"input_tokens":11,"output_tokens":7,"total_tokens":18}`, wantSource: "upstream", wantTotal: 18},
		{name: "absent usage estimates partial text", usageJSON: `null`, wantSource: "estimated", wantTotal: -1},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			body := "data: " + `{"type":"response.output_text.delta","delta":"hello"}` + "\n\n" +
				"data: " + `{"type":"response.incomplete","response":{"status":"incomplete","usage":` + scenario.usageJSON + `}}` + "\n\n"
			ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-5.6-sol", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"}}
			usage, apiErr := OaiResponsesStreamHandler(ginContext, info, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
			require.Nil(test, apiErr)
			require.NotNil(test, usage)
			if scenario.wantTotal >= 0 {
				assert.Equal(test, scenario.wantTotal, usage.TotalTokens)
			} else {
				assert.Positive(test, usage.TotalTokens)
			}
			assert.Equal(test, scenario.wantSource, info.StreamStatus.DeliverySnapshot().UsageSource)
		})
	}
}

func (body *responsesDeliveryBody) Close() error {
	closeErr := body.ReadCloser.Close()
	body.once.Do(func() { close(body.closed) })
	return closeErr
}

type responsesCancelAfterFlushWriter struct {
	gin.ResponseWriter
	cancel         context.CancelFunc
	upstreamClosed <-chan struct{}
}

func (writer *responsesCancelAfterFlushWriter) Flush() {
	writer.ResponseWriter.Flush()
	writer.cancel()
	<-writer.upstreamClosed
}

func TestResponsesTerminalFlushBeforeCancellationPreservesUsageAndCompletion(test *testing.T) {
	gin.SetMode(gin.TestMode)
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	test.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	for _, status := range []string{"completed", "done", "incomplete"} {
		test.Run(status, func(test *testing.T) {
			reader, pipeWriter := io.Pipe()
			body := &responsesDeliveryBody{ReadCloser: reader, closed: make(chan struct{})}
			test.Cleanup(func() { _ = body.Close(); _ = pipeWriter.Close() })
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			requestContext, cancel := context.WithCancel(context.Background())
			test.Cleanup(cancel)
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)
			ginContext.Writer = &responsesCancelAfterFlushWriter{ResponseWriter: ginContext.Writer, cancel: cancel, upstreamClosed: body.closed}
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-5.6-sol", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"}}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}
			finished := make(chan struct{})
			var usage *dto.Usage
			var apiErrorPresent bool
			go func() {
				handlerUsage, apiError := OaiResponsesStreamHandler(ginContext, info, response)
				usage, apiErrorPresent = handlerUsage, apiError != nil
				close(finished)
			}()
			eventType := "response." + status
			responseStatus := status
			if status == "done" {
				responseStatus = "completed"
			}
			_, writeErr := io.WriteString(pipeWriter, `data: {"type":"`+eventType+`","response":{"status":"`+responseStatus+`","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":5}}}}`+"\n\n")
			require.NoError(test, writeErr)
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				test.Fatal("handler did not finish after downstream cancellation")
			}
			require.False(test, apiErrorPresent)
			require.NotNil(test, usage)
			assert.Equal(test, 11, usage.PromptTokens)
			assert.Equal(test, 7, usage.CompletionTokens)
			assert.Equal(test, 5, usage.PromptTokensDetails.CachedTokens)
			assert.Contains(test, recorder.Body.String(), `"type":"`+eventType+`"`)
			require.NotNil(test, info.StreamStatus)
			assert.Equal(test, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
			assert.False(test, info.StreamStatus.HasErrors())
			delivery := info.StreamStatus.DeliverySnapshot()
			assert.Equal(test, eventType, delivery.TerminalEvent)
			assert.Equal(test, responseStatus, delivery.ResponseStatus)
			assert.Equal(test, "flushed", delivery.TerminalWrite)
			assert.Equal(test, "upstream", delivery.UsageSource)
			assert.Positive(test, delivery.ClientCanceledAt)
		})
	}
}
