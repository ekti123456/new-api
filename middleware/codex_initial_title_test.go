package middleware

import (
	"strings"
	"testing"

	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/stretchr/testify/require"
)

func TestInitialTitleExplicitNonzeroRootRemainsLinked(test *testing.T) {
	channel, _, _ := setupCodexRootDistributorTest(test)
	const userID, tokenID = 997301, 997311
	const rootID = "01a08952-0000-7000-8000-000000000721"
	mainRequest, mainRecorder := codexMainRootContext(userID, tokenID, channel.Id, rootID)
	mainRequest.Request.Header.Set("X-Codex-Window-Id", rootID+":71")
	mainRequest.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(mainRequest.GetHeader("X-Codex-Turn-Metadata"), rootID+":0", rootID+":71"))
	Distribute()(mainRequest)
	require.False(test, mainRequest.IsAborted(), mainRecorder.Body.String())
	title, recorder := codexLinkedNamingContext(userID, tokenID, rootID, "01a08952-0000-7000-8000-000000000722", "thread_title")
	Distribute()(title)
	require.False(test, title.IsAborted(), recorder.Body.String())
	require.Equal(test, rootID, relaychannel.ResolveCodexRootSessionForDistribution(title).RootID)
}
