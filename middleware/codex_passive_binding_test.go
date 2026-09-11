package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func independentCodexBackgroundContext(userID, tokenID int, threadID, turnID string) (*gin.Context, *httptest.ResponseRecorder) {
	requestContext, recorder := codexMainRootTurnContext(userID, tokenID, 0, threadID, turnID)
	metadata := strings.ReplaceAll(requestContext.GetHeader("X-Codex-Turn-Metadata"), `"thread_source":"user"`, `"thread_source":"agent_created_thread"`)
	requestContext.Request.Header.Set("X-Codex-Turn-Metadata", metadata)
	requestContext.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
	return requestContext, recorder
}

func TestPassiveRootAssociationChain(test *testing.T) {
	for backendIndex, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			channel, key, _ := setupCodexRootDistributorTest(test)
			if backend == "redis" {
				useCodexRecentRootRedisFixture(test, time.Now())
			}
			userID, tokenID := 993101+backendIndex, 993111
			const mainID = "01a08502-0000-7000-8000-000000000101"
			const backgroundID = "01a08502-0000-7000-8000-000000000102"
			const backgroundTurn = "01a08502-0000-7000-8000-000000000103"
			mainContext, mainRecorder := codexMainRootContext(userID, tokenID, channel.Id, mainID)
			mainContext.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
			Distribute()(mainContext)
			require.False(test, mainContext.IsAborted(), mainRecorder.Body.String())

			first, firstRecorder := independentCodexBackgroundContext(userID, tokenID, backgroundID, backgroundTurn)
			Distribute()(first)
			require.False(test, first.IsAborted(), firstRecorder.Body.String())
			require.True(test, common.GetContextKeyBool(first, constant.ContextKeyCodexRootChannelPinned))
			require.Equal(test, mainID, relaychannel.ResolveCodexRootSessionForDistribution(first).RootID)
			association := relaychannel.CodexRequestRootAssociation(first)
			require.Equal(test, backgroundID, association.OriginalRootID)
			require.Equal(test, service.CodexPrefixRootAssociation, association.Basis)
			require.Equal(test, 1, association.Candidates)

			other, otherRecorder := codexMainRootContext(userID, tokenID, channel.Id, "01a08502-0000-7000-8000-000000000104")
			other.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
			Distribute()(other)
			require.False(test, other.IsAborted(), otherRecorder.Body.String())
			for _, turnID := range []string{backgroundTurn, "01a08502-0000-7000-8000-000000000105"} {
				next, recorder := independentCodexBackgroundContext(userID, tokenID, backgroundID, turnID)
				Distribute()(next)
				require.False(test, next.IsAborted(), recorder.Body.String())
				require.Equal(test, mainID, relaychannel.ResolveCodexRootSessionForDistribution(next).RootID)
				require.Equal(test, key, common.GetContextKeyString(next, constant.ContextKeyChannelKey))
			}

			child, childRecorder := codexLinkedNamingContext(userID, tokenID+1, backgroundID, "01a08502-0000-7000-8000-000000000106", "guardian_review")
			child.Request.Header.Set("X-Codex-Installation-Id", "rotated-device-marker")
			Distribute()(child)
			require.False(test, child.IsAborted(), childRecorder.Body.String())
			require.Equal(test, mainID, relaychannel.ResolveCodexRootSessionForDistribution(child).RootID)
			require.Equal(test, key, common.GetContextKeyString(child, constant.ContextKeyChannelKey))
			mapping, _, found, err := service.ResolveCodexThreadRootBinding(context.Background(), userID, backgroundID)
			require.NoError(test, err)
			require.True(test, found)
			require.Equal(test, mainID, mapping.RootID)
		})
	}
}

func TestPassiveRootAssociationRejectsUnprovenMain(test *testing.T) {
	for scenarioIndex, scenario := range []string{"missing", "other device", "other token", "multiple roots", "explicit missing parent"} {
		test.Run(scenario, func(test *testing.T) {
			channel, _, _ := setupCodexRootDistributorTest(test)
			userID, tokenID := 993201+scenarioIndex, 993211
			const backgroundID = "01a08502-0000-7000-8000-000000000201"
			background, recorder := independentCodexBackgroundContext(userID, tokenID, backgroundID, "01a08502-0000-7000-8000-000000000202")
			if scenario != "missing" {
				mainToken := tokenID
				if scenario == "other token" {
					mainToken++
				}
				main, mainRecorder := codexMainRootContext(userID, mainToken, channel.Id, "01a08502-0000-7000-8000-000000000203")
				main.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
				if scenario == "other device" {
					main.Request.Header.Set("X-Codex-Installation-Id", "another-device")
				}
				Distribute()(main)
				require.False(test, main.IsAborted(), mainRecorder.Body.String())
				if scenario == "multiple roots" {
					other, otherRecorder := codexMainRootContext(userID, tokenID, channel.Id, "01a08502-0000-7000-8000-000000000204")
					other.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
					Distribute()(other)
					require.False(test, other.IsAborted(), otherRecorder.Body.String())
				}
			}
			if scenario == "explicit missing parent" {
				background, recorder = codexLinkedNamingContext(userID, tokenID, backgroundID, "01a08502-0000-7000-8000-000000000205", "guardian_review")
				background.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
			}
			Distribute()(background)
			require.True(test, background.IsAborted())
			require.Equal(test, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.Contains(test, recorder.Body.String(), "codex_background_root_unavailable")
			require.Zero(test, common.GetContextKeyInt(background, constant.ContextKeyChannelId))
			if scenario == "multiple roots" {
				require.Contains(test, recorder.Body.String(), "多个主会话")
			}
		})
	}
}

func TestPassiveRootAssociationConcurrentRequestsKeepOneMain(test *testing.T) {
	channel, _, _ := setupCodexRootDistributorTest(test)
	const userID, tokenID = 993301, 993311
	const rootID = "01a08502-0000-7000-8000-000000000301"
	main, mainRecorder := codexMainRootContext(userID, tokenID, channel.Id, rootID)
	main.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
	Distribute()(main)
	require.False(test, main.IsAborted(), mainRecorder.Body.String())
	first, firstRecorder := independentCodexBackgroundContext(userID, tokenID, "01a08502-0000-7000-8000-000000000302", "01a08502-0000-7000-8000-000000000303")
	second, secondRecorder := independentCodexBackgroundContext(userID, tokenID, "01a08502-0000-7000-8000-000000000302", "01a08502-0000-7000-8000-000000000303")
	var workers sync.WaitGroup
	for _, requestContext := range []*gin.Context{first, second} {
		workers.Add(1)
		go func() { defer workers.Done(); Distribute()(requestContext) }()
	}
	workers.Wait()
	require.False(test, first.IsAborted(), firstRecorder.Body.String())
	require.False(test, second.IsAborted(), secondRecorder.Body.String())
	require.Equal(test, rootID, relaychannel.ResolveCodexRootSessionForDistribution(first).RootID)
	require.Equal(test, rootID, relaychannel.ResolveCodexRootSessionForDistribution(second).RootID)
}
