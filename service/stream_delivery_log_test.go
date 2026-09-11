package service

import (
	"context"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamLogKeepsIncompleteGenerationSeparateFromSuccessfulDelivery(test *testing.T) {
	status := relaycommon.NewStreamStatus()
	status.ObserveTerminal("response.incomplete", "incomplete", "max_output_tokens")
	status.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
	status.RecordTerminalWrite(nil)
	status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	status.SetUsageSource("upstream")
	other := make(map[string]interface{})
	appendStreamStatus(&relaycommon.RelayInfo{IsStream: true, StreamStatus: status}, other)
	stream, ok := other["stream_status"].(map[string]interface{})
	require.True(test, ok)
	assert.Equal(test, "ok", stream["status"])
	delivery, ok := stream["delivery"].(relaycommon.StreamDeliveryStatus)
	require.True(test, ok)
	assert.Equal(test, "incomplete", delivery.ResponseStatus)
	assert.Equal(test, "upstream", delivery.UsageSource)
	assert.Positive(test, delivery.ClientCanceledAt)
}
