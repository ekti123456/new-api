package perfmetrics

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionErrorQueueIsBoundedAndFlushCanBeCancelled(test *testing.T) {
	writer := &admissionErrorWriter{queue: make(chan admissionErrorEvent, 1)}
	writer.once.Do(func() {})
	require.True(test, writer.enqueue(admissionErrorEvent{requestID: "first"}))
	assert.False(test, writer.enqueue(admissionErrorEvent{requestID: "second"}), "overflow must not block request handlers or create unbounded goroutines")
	original := admissionErrors
	admissionErrors = writer
	test.Cleanup(func() { admissionErrors = original })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(test, FlushAdmissionErrors(ctx), context.Canceled)
}

func TestAdmissionErrorMetadataBoundsPreserveUTF8(test *testing.T) {
	actual := admissionErrorText("\r\n中文模型名称\x00", 10)
	assert.True(test, utf8.ValidString(actual))
	assert.LessOrEqual(test, len(actual), 10)
	assert.NotContains(test, actual, "\r")
	assert.NotContains(test, actual, "\n")
	assert.NotContains(test, actual, "\x00")
}
