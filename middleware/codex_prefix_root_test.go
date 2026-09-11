package middleware

import (
	"io"
	"strings"
	"testing"

	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRootlessSourcesUseOnlyScopeAndPrefix(test *testing.T) {
	for index, source := range []string{"thread_title", "system", "thread_summary", "ambient_suggestions", "agent_created_thread", "memory_consolidation", "guardian", "subagent"} {
		test.Run(source, func(test *testing.T) {
			channel, _, _ := setupCodexRootDistributorTest(test)
			userID, tokenID := 998100+index, 771
			mainID := "01a09012" + uuid.NewString()[8:]
			otherID := "01a09013" + uuid.NewString()[8:]
			for _, rootID := range []string{mainID, otherID} {
				main, _ := codexMainRootContext(userID, tokenID, channel.Id, rootID)
				main.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
				Distribute()(main)
				require.False(test, main.IsAborted())
			}
			backgroundID := "01a09012" + uuid.NewString()[8:]
			background, _ := independentCodexBackgroundContext(userID, tokenID, backgroundID, uuid.NewString())
			metadata := background.Request.Header.Get("X-Codex-Turn-Metadata")
			background.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(metadata, "agent_created_thread", source))
			resolved, _, strict, err := resolveUnlinkedCodexPassiveRoot(background, relaychannel.ResolveCodexRootSessionForDistribution(background))
			require.NoError(test, err)
			require.True(test, strict)
			require.True(test, resolved.Related)
			require.Equal(test, mainID, resolved.RootID)
			require.NoError(test, commitCodexPassiveRootAlias(background))
			missing, _ := independentCodexBackgroundContext(userID, tokenID, "01a09999"+uuid.NewString()[8:], uuid.NewString())
			_, _, _, err = resolveUnlinkedCodexPassiveRoot(missing, relaychannel.ResolveCodexRootSessionForDistribution(missing))
			require.Error(test, err)
		})
	}
}

func TestRootlessPrefixRejectsSamePrefixRoots(test *testing.T) {
	channel, _, _ := setupCodexRootDistributorTest(test)
	for range 2 {
		main, _ := codexMainRootContext(998201, 771, channel.Id, "01a09012"+uuid.NewString()[8:])
		main.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
		Distribute()(main)
		require.False(test, main.IsAborted())
	}
	background, _ := independentCodexBackgroundContext(998201, 771, "01a09012"+uuid.NewString()[8:], uuid.NewString())
	_, _, _, err := resolveUnlinkedCodexPassiveRoot(background, relaychannel.ResolveCodexRootSessionForDistribution(background))
	require.ErrorIs(test, err, errCodexBackgroundRootAmbiguous)
}

func TestRootlessPrefixDoesNotReuseLegacyAlias(test *testing.T) {
	channel, _, _ := setupCodexRootDistributorTest(test)
	const userID, tokenID = 998301, 771
	mainID := "01a09012" + uuid.NewString()[8:]
	main, _ := codexMainRootContext(userID, tokenID, channel.Id, mainID)
	main.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
	Distribute()(main)
	require.False(test, main.IsAborted())
	backgroundID := "01a09013" + uuid.NewString()[8:]
	background, _ := independentCodexBackgroundContext(userID, tokenID, backgroundID, uuid.NewString())
	scope := codexPassiveRootScope(background)
	binding, found, err := service.LoadCodexRootChannelBinding(userID, mainID)
	require.NoError(test, err)
	require.True(test, found)
	require.NoError(test, service.StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, mainID, binding, scope))
	alias := service.CodexPassiveRootAlias{RootID: mainID, SelectedGroup: binding.SelectedGroup,
		UARoutingOnly: binding.UARoutingOnly, BindingFingerprint: service.CodexRootChannelBindingFingerprint(binding)}
	require.NoError(test, service.ClaimCodexStrictPassiveRootAlias(test.Context(), userID, tokenID, backgroundID, alias, scope))
	require.NoError(test, service.StoreCodexThreadRootBinding(userID, backgroundID, mainID, binding))
	resolution := relaychannel.ResolveCodexRootSessionForDistribution(background)
	require.NoError(test, service.StoreCodexTurnRootBinding(userID, resolution.TurnID, mainID, binding, service.CodexTurnRouteIdentity{
		Related: true, PassiveFeature: "related_internal", ThreadSource: "agent_created_thread", RequestKind: "turn",
	}))
	_, _, _, err = resolveUnlinkedCodexPassiveRoot(background, relaychannel.ResolveCodexRootSessionForDistribution(background))
	require.Error(test, err)
}

func TestPrefixRoutingRequiresExplicitConsistentSessionID(test *testing.T) {
	for _, scenario := range []string{"body only", "missing", "conflict", "short hash"} {
		test.Run(scenario, func(test *testing.T) {
			setupCodexRootDistributorTest(test)
			sessionID := "01a09012-0000-7000-8000-000000000301"
			requestContext, _ := independentCodexBackgroundContext(998401, 771, sessionID, uuid.NewString())
			switch scenario {
			case "body only":
				requestContext.Request.Header.Del("Session-Id")
				requestContext.Request.Header.Del("X-Codex-Turn-Metadata")
				requestContext.Request.Body = io.NopCloser(strings.NewReader(`{"model":"gpt-5.6-sol","client_metadata":{"session_id":"` + sessionID + `","thread_source":"agent_created_thread"}}`))
			case "missing":
				requestContext.Request.Header.Del("Session-Id")
				requestContext.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+sessionID+`","thread_source":"agent_created_thread","request_kind":"turn"}`)
			case "conflict":
				requestContext.Request.Header.Set("Session-Id", "01a09012-0000-7000-8000-000000000302")
			case "short hash":
				requestContext.Request.Header.Set("Session-Id", strings.ReplaceAll(sessionID, "-", ""))
				requestContext.Request.Header.Del("X-Codex-Turn-Metadata")
			}
			resolution := relaychannel.ResolveCodexRootSessionForDistribution(requestContext)
			if scenario == "body only" {
				require.Equal(test, sessionID, resolution.SessionID)
			} else {
				require.Empty(test, service.CodexSessionIDPrefix(resolution.SessionID))
			}
		})
	}
}
