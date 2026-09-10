package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func initialTitleFixture(test *testing.T, backend string) (CodexPassiveRootScope, CodexRootChannelBinding) {
	test.Helper()
	if backend == "redis" {
		useCodexPassiveRouteRedis(test)
	} else {
		originalEnabled, originalClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = false, nil
		test.Cleanup(func() { common.RedisEnabled, common.RDB = originalEnabled, originalClient })
	}
	return CodexPassiveRootScope{PlatformID: "newapi", UserID: 994701, TokenID: 994711, InstallationID: test.Name()},
		CodexRootChannelBinding{ChannelID: 701, SelectedGroup: "pro", KeyFingerprint: "title-test-key"}
}

func registerInitialTitleFixture(test *testing.T, scope CodexPassiveRootScope, rootID string, binding CodexRootChannelBinding) {
	test.Helper()
	require.NoError(test, StoreCodexRootChannelBinding(scope.UserID, rootID, binding))
	require.NoError(test, StoreProvisionalRecentCodexRootChannelCandidate(scope.UserID, scope.TokenID, rootID, binding, scope))
	require.NoError(test, StoreInitialCodexTitleRootCandidate(scope.UserID, scope.TokenID, rootID, binding, scope))
}

func TestInitialTitleCandidateClaimIsAtomic(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			scope, binding := initialTitleFixture(test, backend)
			rootID := test.Name()
			registerInitialTitleFixture(test, scope, rootID, binding)
			alias := codexPassiveRootAliasForBinding(rootID, binding)
			results := make([]error, 2)
			var workers sync.WaitGroup
			start := make(chan struct{})
			for index, source := range []string{"title-a", "title-b"} {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					results[index] = ClaimCodexInitialTitleRootAlias(context.Background(), scope.UserID, scope.TokenID, source, alias, scope)
				}()
			}
			close(start)
			workers.Wait()
			successes := 0
			for _, result := range results {
				if result == nil {
					successes++
				} else {
					require.ErrorIs(test, result, ErrCodexPassiveRootCandidatesChanged)
				}
			}
			require.Equal(test, 1, successes)
			candidates, err := LoadCodexInitialTitleRootCandidates(context.Background(), scope.UserID, scope.TokenID, scope)
			require.NoError(test, err)
			require.Empty(test, candidates)
		})
	}
}

func TestInitialTitleCandidateChangedSetDoesNotConsumeRoot(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			scope, binding := initialTitleFixture(test, backend)
			rootID, otherRoot := test.Name(), test.Name()+"-other"
			registerInitialTitleFixture(test, scope, rootID, binding)
			candidates, err := LoadCodexInitialTitleRootCandidates(context.Background(), scope.UserID, scope.TokenID, scope)
			require.NoError(test, err)
			require.Len(test, candidates, 1)
			registerInitialTitleFixture(test, scope, otherRoot, binding)
			alias := codexPassiveRootAliasForBinding(rootID, binding)
			err = ClaimCodexInitialTitleRootAlias(context.Background(), scope.UserID, scope.TokenID, "new-title", alias, scope)
			require.ErrorIs(test, err, ErrCodexPassiveRootCandidatesChanged)
			require.NoError(test, MarkCodexInitialTitleRootNamed(context.Background(), scope.UserID, otherRoot))
			require.NoError(test, ClaimCodexInitialTitleRootAlias(context.Background(), scope.UserID, scope.TokenID, "new-title", alias, scope))
		})
	}
}

func TestInitialTitleNamedRootCannotBeRepublished(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		for _, phase := range []string{"before publication", "after publication"} {
			test.Run(backend+"/"+phase, func(test *testing.T) {
				scope, binding := initialTitleFixture(test, backend)
				rootID := test.Name()
				if phase == "before publication" {
					require.NoError(test, MarkCodexInitialTitleRootNamed(context.Background(), scope.UserID, rootID))
				}
				registerInitialTitleFixture(test, scope, rootID, binding)
				require.NoError(test, MarkCodexInitialTitleRootNamed(context.Background(), scope.UserID, rootID))
				require.NoError(test, StoreInitialCodexTitleRootCandidate(scope.UserID, scope.TokenID, rootID, binding, scope))
				candidates, err := LoadCodexInitialTitleRootCandidates(context.Background(), scope.UserID, scope.TokenID, scope)
				require.NoError(test, err)
				require.Empty(test, candidates)
				otherScope := scope
				otherScope.TokenID++
				otherScope.InstallationID = "rotated-device"
				registerInitialTitleFixture(test, otherScope, rootID, binding)
				candidates, err = LoadCodexInitialTitleRootCandidates(context.Background(), otherScope.UserID, otherScope.TokenID, otherScope)
				require.NoError(test, err)
				require.Empty(test, candidates)
			})
		}
	}
}

func TestInitialTitleCandidateLifetimeDoesNotRefresh(test *testing.T) {
	server := useCodexPassiveRouteRedis(test)
	scope := CodexPassiveRootScope{UserID: 994801, TokenID: 994811, InstallationID: test.Name()}
	binding := CodexRootChannelBinding{ChannelID: 801, SelectedGroup: "pro", KeyFingerprint: "expiry-test-key"}
	rootID := test.Name()
	registerInitialTitleFixture(test, scope, rootID, binding)
	server.FastForward(6 * time.Second)
	require.NoError(test, StoreInitialCodexTitleRootCandidate(scope.UserID, scope.TokenID, rootID, binding, scope))
	require.NoError(test, StoreProvisionalCodexTitleRootChannelCandidate(scope.UserID, scope.TokenID, rootID, binding, scope))
	candidates, err := LoadCodexInitialTitleRootCandidates(context.Background(), scope.UserID, scope.TokenID, scope)
	require.NoError(test, err)
	require.Empty(test, candidates)
	ambientCandidates, err := LoadCodexTitleRootChannelCandidates(context.Background(), scope.UserID, scope.TokenID, scope)
	require.NoError(test, err)
	require.Len(test, ambientCandidates, 1)
}
