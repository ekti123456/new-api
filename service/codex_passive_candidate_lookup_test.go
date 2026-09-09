package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func TestPassiveCandidateLookupCachesMissAndInvalidatesOnMainPublication(test *testing.T) {
	server := useCodexPassiveRouteRedis(test)
	const userID, tokenID = 994101, 994102
	scope := CodexPassiveRootScope{PlatformID: "newapi", UserID: userID, TokenID: tokenID, InstallationID: "candidate-cache-device"}
	first, err := LoadCodexPassiveRootCandidates(test.Context(), userID, tokenID, false, scope)
	require.NoError(test, err)
	require.Empty(test, first)
	commands := server.CommandCount()
	second, err := LoadCodexPassiveRootCandidates(test.Context(), userID, tokenID, false, scope)
	require.NoError(test, err)
	require.Empty(test, second)
	require.Equal(test, commands, server.CommandCount(), "waiting requests must not repeat the same empty cache query")
	const rootID = "01a08502-0000-7000-8000-000000000501"
	binding := CodexRootChannelBinding{ChannelID: 1, SelectedGroup: "pro", KeyFingerprint: "main-key"}
	require.NoError(test, StoreCodexRootChannelBinding(userID, rootID, binding))
	require.NoError(test, StoreRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, scope))
	third, err := LoadCodexPassiveRootCandidates(test.Context(), userID, tokenID, false, scope)
	require.NoError(test, err)
	require.Len(test, third, 1)
	require.Equal(test, rootID, third[0].RootID)
	otherScope := scope
	otherScope.InstallationID = "other-device"
	other, err := LoadCodexPassiveRootCandidates(test.Context(), userID, tokenID, false, otherScope)
	require.NoError(test, err)
	require.Empty(test, other)
	cancelled, cancel := context.WithCancel(test.Context())
	cancel()
	_, err = LoadCodexPassiveRootCandidates(cancelled, userID, tokenID, false, scope)
	require.ErrorIs(test, err, context.Canceled)
}

func TestStrictPassiveAliasChecksBothRoutingSidesAtomically(test *testing.T) {
	for backendIndex, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			if backend == "redis" {
				useCodexPassiveRouteRedis(test)
			} else {
				previous := common.RedisEnabled
				common.RedisEnabled = false
				test.Cleanup(func() { common.RedisEnabled = previous })
			}
			userID, tokenID := 994201+backendIndex, 994211
			scope := CodexPassiveRootScope{PlatformID: "newapi", UserID: userID, TokenID: tokenID, InstallationID: "atomic-device"}
			const mainID = "01a08502-0000-7000-8000-000000000601"
			const otherID = "01a08502-0000-7000-8000-000000000602"
			const backgroundID = "01a08502-0000-7000-8000-000000000603"
			binding := CodexRootChannelBinding{ChannelID: 1, SelectedGroup: "pro", KeyFingerprint: "main-key"}
			require.NoError(test, StoreCodexRootChannelBinding(userID, mainID, binding))
			require.NoError(test, StoreRecentCodexRootChannelCandidate(userID, tokenID, mainID, binding, scope))
			candidates, err := LoadCodexPassiveRootCandidates(test.Context(), userID, tokenID, false, scope)
			require.NoError(test, err)
			require.Len(test, candidates, 1)
			alias := codexPassiveRootAliasForBinding(mainID, binding)
			other := binding
			other.ChannelID = 2
			other.UARoutingOnly = true
			require.NoError(test, StoreCodexRootChannelBinding(userID, otherID, other))
			require.NoError(test, StoreRecentCodexRootChannelCandidate(userID, tokenID, otherID, other, scope))
			require.ErrorIs(test, ClaimCodexStrictPassiveRootAlias(test.Context(), userID, tokenID, backgroundID, alias, scope), ErrCodexPassiveRootCandidatesChanged)
			_, found, err := LoadCodexPassiveRootAlias(test.Context(), userID, tokenID, backgroundID, scope)
			require.NoError(test, err)
			require.False(test, found)
		})
	}
}
