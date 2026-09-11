package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func prefixRootFixture(test *testing.T, backend string) (CodexPassiveRootScope, CodexRootChannelBinding) {
	test.Helper()
	scope, binding := initialTitleFixture(test, backend)
	scope.InstallationID += ":" + uuid.NewString()
	return scope, binding
}

func TestCodexPrefixRootSelectionAndAtomicClaim(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			scope, binding := prefixRootFixture(test, backend)
			rootID := "01a09012" + uuid.NewString()[8:]
			backgroundID := "01a09012" + uuid.NewString()[8:]
			otherRoot := "01a09013" + uuid.NewString()[8:]
			ctx := test.Context()
			for _, candidate := range []string{rootID, otherRoot} {
				require.NoError(test, StoreCodexRootChannelBinding(scope.UserID, candidate, binding))
				require.NoError(test, StoreCodexPrefixRootCandidate(ctx, scope, candidate, candidate, binding))
			}
			candidates, err := LoadCodexPrefixRootCandidates(ctx, scope, backgroundID)
			require.NoError(test, err)
			require.Len(test, candidates, 1)
			require.Equal(test, rootID, candidates[0].RootID)
			alias := codexPassiveRootAliasForBinding(rootID, binding)
			alias.Association = CodexPrefixRootAssociation
			require.NoError(test, ClaimCodexPrefixRootAlias(ctx, scope, backgroundID, backgroundID, alias))
			require.NoError(test, PromoteCodexPassiveRootAlias(ctx, scope.UserID, scope.TokenID, backgroundID, alias, scope))
			stored, found, err := LoadCodexPassiveRootAlias(ctx, scope.UserID, scope.TokenID, backgroundID, scope)
			require.NoError(test, err)
			require.True(test, found)
			require.Equal(test, alias, stored)
			collision := "01a09012" + uuid.NewString()[8:]
			require.NoError(test, StoreCodexRootChannelBinding(scope.UserID, collision, binding))
			require.NoError(test, StoreCodexPrefixRootCandidate(ctx, scope, collision, collision, binding))
			candidates, err = LoadCodexPrefixRootCandidates(ctx, scope, backgroundID)
			require.NoError(test, err)
			require.Len(test, candidates, 2)
			require.ErrorIs(test, ClaimCodexPrefixRootAlias(ctx, scope, backgroundID, "new-source", alias), ErrCodexPassiveRootCandidatesChanged)
			require.NoError(test, ClaimCodexPrefixRootAlias(ctx, scope, backgroundID, backgroundID, alias))
		})
	}
}

func TestCodexPrefixRootIsolationAndMissingIdentity(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			scope, binding := prefixRootFixture(test, backend)
			rootID := "01a09012" + uuid.NewString()[8:]
			require.NoError(test, StoreCodexRootChannelBinding(scope.UserID, rootID, binding))
			require.NoError(test, StoreCodexPrefixRootCandidate(test.Context(), scope, rootID, rootID, binding))
			for _, field := range []string{"user", "token", "platform", "device", "prefix"} {
				test.Run(field, func(test *testing.T) {
					otherScope, sessionID := scope, rootID
					switch field {
					case "user":
						otherScope.UserID++
					case "token":
						otherScope.TokenID++
					case "platform":
						otherScope.PlatformID += "-other"
					case "device":
						otherScope.InstallationID += "-other"
					case "prefix":
						sessionID = "01a09013" + rootID[8:]
					}
					candidates, err := LoadCodexPrefixRootCandidates(test.Context(), otherScope, sessionID)
					require.NoError(test, err)
					require.Empty(test, candidates)
				})
			}
			for _, invalid := range []string{"", "01a09012", "01a09012000070008000000000000001", "bad-session"} {
				_, err := LoadCodexPrefixRootCandidates(test.Context(), scope, invalid)
				require.ErrorIs(test, err, ErrCodexSessionPrefixUnavailable)
			}
			candidates, err := LoadCodexPrefixRootCandidates(test.Context(), scope, "01A09012"+rootID[8:])
			require.NoError(test, err)
			require.Len(test, candidates, 1)
		})
	}
}

func TestCodexPrefixRootKeepsUnavailableAndOverflowAmbiguous(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			scope, binding := prefixRootFixture(test, backend)
			sessionID := "01a09012-0000-7000-8000-000000000001"
			for index := 0; index <= codexPrefixRootLimit; index++ {
				err := StoreCodexPrefixRootCandidate(test.Context(), scope, sessionID, fmt.Sprintf("unavailable-%d", index), binding)
				if index == codexPrefixRootLimit {
					require.ErrorIs(test, err, ErrCodexPrefixRootAmbiguous)
				} else {
					require.NoError(test, err)
				}
				if index == 0 {
					_, err := LoadCodexPrefixRootCandidates(test.Context(), scope, sessionID)
					require.ErrorIs(test, err, ErrCodexRecentRootBindingUnavailable)
				}
				if index == 1 {
					candidates, err := LoadCodexPrefixRootCandidates(test.Context(), scope, sessionID)
					require.NoError(test, err)
					require.Len(test, candidates, 2)
				}
			}
			_, err := LoadCodexPrefixRootCandidates(test.Context(), scope, sessionID)
			require.ErrorIs(test, err, ErrCodexPrefixRootAmbiguous)
		})
	}
}

func TestCodexPrefixTitleAllowsConcurrentNamesAndHistoricalClaims(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			scope, binding := prefixRootFixture(test, backend)
			rootID := "01a09012" + uuid.NewString()[8:]
			require.NoError(test, StoreCodexRootChannelBinding(scope.UserID, rootID, binding))
			require.NoError(test, StoreCodexPrefixRootCandidate(test.Context(), scope, rootID, rootID, binding))
			alias := codexPassiveRootAliasForBinding(rootID, binding)
			alias.Association = CodexPrefixRootAssociation
			var workers sync.WaitGroup
			results := make([]error, 2)
			sourceIDs := []string{"01a09012" + uuid.NewString()[8:], "01a09012" + uuid.NewString()[8:]}
			for index := range results {
				workers.Add(1)
				go func() {
					defer workers.Done()
					sourceID := sourceIDs[index]
					results[index] = ClaimCodexPrefixRootAlias(test.Context(), scope, sourceID, sourceID, alias)
				}()
			}
			workers.Wait()
			for index, result := range results {
				require.NoError(test, result)
				stored, found, err := LoadCodexPassiveRootAlias(test.Context(), scope.UserID, scope.TokenID, sourceIDs[index], scope)
				require.NoError(test, err)
				require.True(test, found)
				require.Equal(test, alias, stored)
			}
			require.NoError(test, MarkCodexInitialTitleRootNamed(test.Context(), scope.UserID, rootID))
			require.NoError(test, ClaimCodexPrefixRootAlias(test.Context(), scope, rootID, "third-title", alias))
		})
	}
}

func TestCodexPrefixRootOutlivesTemporalCandidates(test *testing.T) {
	server := useCodexPassiveRouteRedis(test)
	scope := CodexPassiveRootScope{UserID: 881931, TokenID: 2, InstallationID: test.Name()}
	binding := CodexRootChannelBinding{ChannelID: 701, SelectedGroup: "pro", KeyFingerprint: "prefix-key"}
	rootID := "01a09012" + uuid.NewString()[8:]
	require.NoError(test, StoreCodexRootChannelBinding(scope.UserID, rootID, binding))
	require.NoError(test, StoreCodexPrefixRootCandidate(test.Context(), scope, rootID, rootID, binding))
	server.FastForward(2 * time.Hour)
	candidates, err := LoadCodexPrefixRootCandidates(context.Background(), scope, rootID)
	require.NoError(test, err)
	require.Len(test, candidates, 1)
	require.Greater(test, common.RDB.TTL(test.Context(), codexPrefixRootKey(scope, rootID)).Val(), time.Hour)
}
