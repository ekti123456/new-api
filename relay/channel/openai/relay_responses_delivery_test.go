package openai

import (
	"context"
	"errors"
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

func (body *responsesDeliveryBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(func() { close(body.closed) })
	return err
}

type responsesDeliveryWriter struct {
	gin.ResponseWriter
	cancelOnFlush  context.CancelFunc
	cancelOnWrite  context.CancelFunc
	upstreamClosed <-chan struct{}
	writeError     error
}

func (writer *responsesDeliveryWriter) Flush() {
	writer.ResponseWriter.Flush()
	if writer.cancelOnFlush != nil {
		writer.cancelOnFlush()
		<-writer.upstreamClosed
	}
}

func (writer *responsesDeliveryWriter) WriteString(value string) (int, error) {
	if writer.cancelOnWrite != nil && strings.HasPrefix(value, "data:") {
		writer.cancelOnWrite()
		<-writer.upstreamClosed
		return 0, context.Canceled
	}
	if writer.writeError != nil {
		return 0, writer.writeError
	}
	return writer.ResponseWriter.WriteString(value)
}

func (writer *responsesDeliveryWriter) Write(value []byte) (int, error) {
	if writer.writeError != nil {
		return 0, writer.writeError
	}
	return writer.ResponseWriter.Write(value)
}

func TestResponsesTerminalDelivery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, scenario := range []struct {
		name          string
		event         string
		status        string
		cancelOnFlush bool
		cancelOnWrite bool
		writeError    bool
		wantEnd       relaycommon.StreamEndReason
	}{
		{name: "completed_without_eof", event: "response.completed", status: "completed", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "done_without_eof", event: "response.done", status: "completed", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "incomplete_without_eof", event: "response.incomplete", status: "incomplete", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "failed_without_eof", event: "response.failed", status: "failed", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "error_without_eof", event: "response.error", status: "failed", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "standalone_error_without_eof", event: "error", status: "failed", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "cancelled_without_eof", event: "response.cancelled", status: "cancelled", wantEnd: relaycommon.StreamEndReasonDone},
		{name: "cancel_after_terminal_flush", event: "response.completed", status: "completed", cancelOnFlush: true, wantEnd: relaycommon.StreamEndReasonDone},
		{name: "incomplete_then_cancel", event: "response.incomplete", status: "incomplete", cancelOnFlush: true, wantEnd: relaycommon.StreamEndReasonDone},
		{name: "cancel_before_terminal_data", event: "response.completed", status: "completed", cancelOnWrite: true, wantEnd: relaycommon.StreamEndReasonClientGone},
		{name: "terminal_write_failed", event: "response.completed", status: "completed", writeError: true, wantEnd: relaycommon.StreamEndReasonHandlerStop},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader, pipeWriter := io.Pipe()
			body := &responsesDeliveryBody{ReadCloser: reader, closed: make(chan struct{})}
			requestContext, cancel := context.WithCancel(context.Background())
			t.Cleanup(func() { cancel(); _ = body.Close(); _ = pipeWriter.Close() })
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)
			writer := &responsesDeliveryWriter{ResponseWriter: ctx.Writer, upstreamClosed: body.closed}
			if scenario.cancelOnFlush {
				writer.cancelOnFlush = cancel
			}
			if scenario.cancelOnWrite {
				writer.cancelOnWrite = cancel
			}
			if scenario.writeError {
				writer.writeError = errors.New("downstream write failed")
			}
			ctx.Writer = writer
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-6-astra", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"}}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
			finished := make(chan struct{})
			var usage *dto.Usage
			var apiErrorPresent bool
			go func() {
				result, apiErr := OaiResponsesStreamHandler(ctx, info, response)
				usage, apiErrorPresent = result, apiErr != nil
				close(finished)
			}()
			payload := `{"type":"` + scenario.event + `","response":{"status":"` + scenario.status + `","output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":5}}}}`
			wantInput, wantOutput, wantCache := 11, 7, 5
			if scenario.event == "error" || scenario.event == "response.error" {
				payload = `{"type":"` + scenario.event + `","code":"upstream_error","message":"upstream rejected the request"}`
				wantInput, wantOutput, wantCache = 0, 0, 0
			}
			_, err := io.WriteString(pipeWriter, "event: "+scenario.event+"\ndata: "+payload+"\n\n")
			require.NoError(t, err)
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				cancel()
				_ = body.Close()
				<-finished
				t.Fatal("Responses terminal did not finish without waiting for upstream EOF")
			}
			require.False(t, apiErrorPresent)
			require.NotNil(t, usage)
			assert.Equal(t, wantInput, usage.PromptTokens)
			assert.Equal(t, wantOutput, usage.CompletionTokens)
			assert.Equal(t, wantCache, usage.PromptTokensDetails.CachedTokens)
			require.NotNil(t, info.StreamStatus)
			assert.Equal(t, scenario.wantEnd, info.StreamStatus.EndReason)
			assert.Equal(t, scenario.writeError || scenario.cancelOnWrite, info.StreamStatus.HasErrors())
			assert.Equal(t, scenario.status, info.StreamStatus.ResponseOutcome())
			assert.Equal(t, scenario.status == "failed", info.StreamStatus.ResponseFailed(), "transport completion must not turn an upstream failure into success")
			delivery := info.StreamStatus.DeliverySnapshot()
			assert.Equal(t, scenario.event, delivery.TerminalEvent)
			assert.Positive(t, delivery.TerminalReceivedAt)
			if scenario.writeError || scenario.cancelOnWrite {
				assert.Equal(t, "failed", delivery.TerminalWrite)
				assert.Zero(t, delivery.TerminalFlushedAt)
			} else {
				assert.Equal(t, "flushed", delivery.TerminalWrite)
				assert.Positive(t, delivery.TerminalFlushedAt)
			}
			if scenario.cancelOnWrite || scenario.cancelOnFlush {
				assert.Positive(t, delivery.ClientCanceledAt)
			}
			if scenario.writeError {
				assert.Contains(t, info.StreamStatus.Summary(), "downstream write failed")
			} else if !scenario.cancelOnWrite {
				assert.Contains(t, recorder.Body.String(), payload)
			}
		})
	}
}

func TestResponsesPartialOutputCancellationRemainsFailure(t *testing.T) {
	service.InitTokenEncoders()
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	reader, pipeWriter := io.Pipe()
	body := &responsesDeliveryBody{ReadCloser: reader, closed: make(chan struct{})}
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = body.Close(); _ = pipeWriter.Close() })
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)
	ctx.Writer = &responsesDeliveryWriter{ResponseWriter: ctx.Writer, cancelOnFlush: cancel, upstreamClosed: body.closed}
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-6-astra", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"}}
	finished := make(chan struct{})
	go func() {
		_, _ = OaiResponsesStreamHandler(ctx, info, &http.Response{StatusCode: http.StatusOK, Body: body})
		close(finished)
	}()
	_, err := io.WriteString(pipeWriter, "data: "+`{"type":"response.output_text.delta","delta":"partial output"}`+"\n\n")
	require.NoError(t, err)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled partial response did not stop")
	}
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.False(t, info.StreamStatus.IsNormalEnd())
	assert.Empty(t, strings.TrimSpace(info.StreamStatus.ResponseOutcome()))
}

type responsesTransportFailureRecorder struct {
	*httptest.ResponseRecorder
	flushErr error
}

func (writer *responsesTransportFailureRecorder) FlushError() error {
	if writer.flushErr != nil {
		return writer.flushErr
	}
	writer.ResponseRecorder.Flush()
	return nil
}

func TestResponsesTerminalTransportErrorThroughGin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	recorder := &responsesTransportFailureRecorder{ResponseRecorder: httptest.NewRecorder(), flushErr: errors.New("transport flush failed")}
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-6-astra", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"}}
	payload := `{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`
	usage, apiErr := OaiResponsesStreamHandler(ctx, info, &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: " + payload + "\n\n"))})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
	require.NotNil(t, info.StreamStatus)
	assert.True(t, info.StreamStatus.HasErrors())
	require.Len(t, info.StreamStatus.Errors, 1)
	assert.Contains(t, info.StreamStatus.Errors[0].Message, "transport flush failed")
	assert.Equal(t, "failed", info.StreamStatus.DeliverySnapshot().TerminalWrite)
	assert.Zero(t, info.StreamStatus.DeliverySnapshot().TerminalFlushedAt)
}

func TestResponsesNamedToolEventsPreservePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	events := []struct{ name, data string }{
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"lookup","arguments":""}}`},
		{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"item_1","delta":"{\"city\":\"Paris\"}"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"lookup","arguments":"{\"city\":\"Paris\"}"}}`},
		{"response.completed", `{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`},
	}
	var input strings.Builder
	for _, event := range events {
		input.WriteString("event: " + event.name + "\ndata: " + event.data + "\n\n")
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-6-astra", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-6-astra"}}
	usage, apiErr := OaiResponsesStreamHandler(ctx, info, &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(input.String()))})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
	assert.Equal(t, input.String(), recorder.Body.String())
	assert.False(t, info.StreamStatus.HasErrors())
	assert.True(t, info.StreamStatus.IsNormalEnd())
	assert.Equal(t, "flushed", info.StreamStatus.DeliverySnapshot().TerminalWrite)
}
