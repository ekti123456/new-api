package common

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamDeliveryOnlySuccessfulTerminalWriteOverridesCancellation(test *testing.T) {
	for _, scenario := range []struct {
		name       string
		write      bool
		writeErr   error
		wantReason StreamEndReason
	}{
		{name: "received but not written", wantReason: StreamEndReasonClientGone},
		{name: "write failed", write: true, writeErr: errors.New("broken pipe"), wantReason: StreamEndReasonClientGone},
		{name: "terminal flushed", write: true, wantReason: StreamEndReasonDone},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			status := NewStreamStatus()
			status.ObserveTerminal("response.incomplete", "incomplete", "max_output_tokens")
			status.SetEndReason(StreamEndReasonClientGone, context.Canceled)
			if scenario.write {
				status.RecordTerminalWrite(scenario.writeErr)
			}
			status.SetEndReason(StreamEndReasonDone, nil)
			assert.Equal(test, scenario.wantReason, status.EndReason)
			delivery := status.DeliverySnapshot()
			assert.Equal(test, "incomplete", delivery.ResponseStatus)
			assert.Positive(test, delivery.ClientCanceledAt)
		})
	}
}
