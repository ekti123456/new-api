package middleware

import (
	"context"
	"strings"
	"testing"
	"time"

	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestInitialTitleUsesSessionPrefixWithoutWindowZeroHeuristic(test *testing.T) {
	for scenarioIndex, scenario := range []string{"new zero", "nonzero", "missing window", "window conflict", "compaction", "already bound"} {
		test.Run(scenario, func(test *testing.T) {
			channel, _, fingerprint := setupCodexRootDistributorTest(test)
			userID, tokenID := 997101+scenarioIndex, 997111
			const rootID = "01a08952-0000-7000-8000-000000000701"
			requestContext, recorder := codexMainRootContext(userID, tokenID, channel.Id, rootID)
			metadata := requestContext.GetHeader("X-Codex-Turn-Metadata")
			switch scenario {
			case "nonzero":
				requestContext.Request.Header.Set("X-Codex-Window-Id", rootID+":11")
				metadata = strings.ReplaceAll(metadata, rootID+":0", rootID+":11")
			case "missing window":
				requestContext.Request.Header.Del("X-Codex-Window-Id")
				metadata = strings.ReplaceAll(metadata, `"window_id":"`+rootID+`:0",`, "")
			case "window conflict":
				requestContext.Request.Header.Set("X-Codex-Window-Id", rootID+":1")
			case "compaction":
				metadata = strings.ReplaceAll(metadata, `"request_kind":"turn"`, `"request_kind":"compaction"`)
			case "already bound":
				require.NoError(test, service.StoreCodexRootChannelBinding(userID, rootID, service.CodexRootChannelBinding{
					ChannelID: channel.Id, SelectedGroup: "pro", KeyFingerprint: fingerprint,
				}))
			}
			requestContext.Request.Header.Set("X-Codex-Turn-Metadata", metadata)
			Distribute()(requestContext)
			require.False(test, requestContext.IsAborted(), recorder.Body.String())
			title, titleRecorder := codexUnlinkedNativeTitleContext(userID, tokenID, "01a08952-0000-7000-8000-000000000702")
			Distribute()(title)
			require.False(test, title.IsAborted(), titleRecorder.Body.String())
			require.Equal(test, rootID, relaychannel.ResolveCodexRootSessionForDistribution(title).RootID)
		})
	}
}

func TestInitialTitleOldContinuationDoesNotStealNewTitle(test *testing.T) {
	channel, _, _ := setupCodexRootDistributorTest(test)
	server, _ := useCodexRecentRootRedisFixture(test, time.Now())
	const userID, tokenID = 997201, 997211
	const oldRoot = "01a08951-0000-7000-8000-000000000711"
	const newRoot = "01a08952-0000-7000-8000-000000000712"
	mainRequest, mainRecorder := codexMainRootContext(userID, tokenID, channel.Id, oldRoot)
	Distribute()(mainRequest)
	require.False(test, mainRequest.IsAborted(), mainRecorder.Body.String())
	server.FastForward(6 * time.Second)
	continuation, continuationRecorder := codexMainRootContext(userID, tokenID, channel.Id, oldRoot)
	Distribute()(continuation)
	require.False(test, continuation.IsAborted(), continuationRecorder.Body.String())
	waitCalls := 0
	waitForRecentCodexTitleRootUpdate = func(ctx context.Context, actualUser, actualToken int, duration time.Duration) error {
		waitCalls++
		require.Equal(test, userID, actualUser)
		require.Equal(test, tokenID, actualToken)
		if waitCalls == 1 {
			newMain, recorder := codexMainRootContext(userID, tokenID, channel.Id, newRoot)
			Distribute()(newMain)
			require.False(test, newMain.IsAborted(), recorder.Body.String())
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	title, titleRecorder := codexUnlinkedNativeTitleContext(userID, tokenID, "01a08952-0000-7000-8000-000000000713")
	Distribute()(title)
	require.False(test, title.IsAborted(), titleRecorder.Body.String())
	require.Equal(test, 1, waitCalls)
	require.Equal(test, newRoot, relaychannel.ResolveCodexRootSessionForDistribution(title).RootID)
	ambient, ambientRecorder := codexMainRootContext(userID, tokenID, 0, "01a08951-0000-7000-8000-000000000714")
	ambient.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(ambient.GetHeader("X-Codex-Turn-Metadata"), `"thread_source":"user"`, `"thread_source":"ambient_suggestions"`))
	Distribute()(ambient)
	require.False(test, ambient.IsAborted(), ambientRecorder.Body.String())
	require.Equal(test, oldRoot, relaychannel.ResolveCodexRootSessionForDistribution(ambient).RootID)
}

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
