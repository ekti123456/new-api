package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func useCodexLineageMemory(t *testing.T) {
	t.Helper()
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	codexRootChannelCacheOnce = sync.Once{}
	codexRootChannelCache = nil
	codexLegacyRootChannelCacheOnce = sync.Once{}
	codexLegacyRootChannelCache = nil
	codexRootChannelMemoryExpiresAt = make(map[string]time.Time)
	codexTurnRootCacheOnce = sync.Once{}
	codexTurnRootCache = nil
	codexTurnRootMemoryExpiresAt = make(map[string]time.Time)
	codexTurnRootRollbackOwners = make(map[string]codexLineageRollbackOwner)
	codexTurnRootWaiters.Lock()
	codexTurnRootWaiters.items = make(map[string]*codexRootChannelWaiter)
	codexTurnRootWaiters.Unlock()
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
		codexRootChannelCacheOnce = sync.Once{}
		codexRootChannelCache = nil
		codexLegacyRootChannelCacheOnce = sync.Once{}
		codexLegacyRootChannelCache = nil
		codexRootChannelMemoryExpiresAt = make(map[string]time.Time)
		codexTurnRootCacheOnce = sync.Once{}
		codexTurnRootCache = nil
		codexTurnRootMemoryExpiresAt = make(map[string]time.Time)
		codexTurnRootRollbackOwners = make(map[string]codexLineageRollbackOwner)
		codexTurnRootWaiters.Lock()
		codexTurnRootWaiters.items = make(map[string]*codexRootChannelWaiter)
		codexTurnRootWaiters.Unlock()
	})
}

func TestCodexTurnRootBindingIsUserScopedAndRevalidatesRootRoute(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const (
		userID = 46001
		rootID = "01a06010-0000-7000-8000-000000000001"
		turnID = "01a06010-0000-7000-8000-000000000002"
	)
	rootBinding := CodexRootChannelBinding{
		ChannelID: 901, SelectedGroup: "pro", KeyIndex: 2, KeyFingerprint: "abcdef0123456789", UARoutingOnly: true,
	}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, rootBinding))

	rootRole := CodexTurnRouteIdentity{RootOwner: true, ThreadSource: "user", RequestKind: "turn"}
	winner, won, _, _, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
	require.NoError(t, err)
	require.True(t, won)
	require.Equal(t, rootID, winner.RootID)
	require.Equal(t, CodexRootChannelBindingFingerprint(rootBinding), winner.BindingFingerprint)

	mapping, resolvedRoot, found, err := ResolveCodexTurnRootBinding(context.Background(), userID, turnID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, rootID, mapping.RootID)
	require.Equal(t, rootBinding, resolvedRoot)

	_, _, found, err = ResolveCodexTurnRootBinding(context.Background(), userID+1, turnID)
	require.NoError(t, err)
	require.False(t, found)
}

func TestCodexTurnRootBindingPromotionDoesNotShrinkOnRetry(t *testing.T) {
	server := useCodexPassiveRouteRedis(t)
	const (
		userID = 46002
		rootID = "01a06010-0000-7000-8000-000000000011"
		turnID = "01a06010-0000-7000-8000-000000000012"
	)
	rootBinding := CodexRootChannelBinding{
		ChannelID: 902, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789",
	}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, rootBinding))
	rootRole := CodexTurnRouteIdentity{RootOwner: true, ThreadSource: "user", RequestKind: "turn"}
	_, won, _, _, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
	require.NoError(t, err)
	require.True(t, won)

	key := getCodexTurnRootCache().FullKey(codexTurnRootCacheKey(userID, turnID))
	require.LessOrEqual(t, server.TTL(key), codexProvisionalRootChannelCacheTTL)
	require.NoError(t, StoreCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole))
	durableTTL := server.TTL(key)
	require.Greater(t, durableTTL, 23*time.Hour)

	_, won, _, _, err = ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
	require.NoError(t, err)
	require.True(t, won)
	require.Greater(t, server.TTL(key), 23*time.Hour)
	require.LessOrEqual(t, server.TTL(key), durableTTL)
}

func TestCodexTurnRootBindingRejectsConflictingTurnAndReboundRoot(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const (
		userID = 46003
		rootA  = "01a06010-0000-7000-8000-000000000021"
		rootB  = "01a06010-0000-7000-8000-000000000022"
		turnID = "01a06010-0000-7000-8000-000000000023"
	)
	bindingA := CodexRootChannelBinding{ChannelID: 903, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	bindingB := CodexRootChannelBinding{ChannelID: 904, SelectedGroup: "pro", KeyFingerprint: "key-b"}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootA, bindingA))
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootB, bindingB))
	rootRole := CodexTurnRouteIdentity{RootOwner: true, ThreadSource: "user", RequestKind: "turn"}
	_, won, _, _, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootA, bindingA, rootRole)
	require.NoError(t, err)
	require.True(t, won)

	winner, won, _, _, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootB, bindingB, rootRole)
	require.NoError(t, err)
	require.False(t, won)
	require.Equal(t, rootA, winner.RootID)

	rebound := CodexRootChannelBinding{ChannelID: 905, SelectedGroup: "pro", KeyFingerprint: "key-c"}
	reboundPayload, err := common.Marshal(rebound)
	require.NoError(t, err)
	rootKey := getCodexRootChannelCache().FullKey(codexRootChannelCacheKey(userID, rootA, false))
	require.NoError(t, common.RDB.Set(context.Background(), rootKey, reboundPayload, time.Hour).Err())

	_, _, found, err := ResolveCodexTurnRootBinding(context.Background(), userID, turnID)
	require.True(t, found)
	require.ErrorIs(t, err, ErrCodexTurnRootRouteUnavailable)
}

func TestCodexTurnRootBindingPreservesPassiveRetryRole(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const (
		userID = 46004
		rootID = "01a06010-0000-7000-8000-000000000031"
		turnID = "01a06010-0000-7000-8000-000000000032"
	)
	rootBinding := CodexRootChannelBinding{
		ChannelID: 906, SelectedGroup: "pro", KeyFingerprint: "passive-key",
	}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, rootBinding))
	passiveRole := CodexTurnRouteIdentity{
		Related: true, PassiveFeature: "system_passive", ThreadSource: "system", RequestKind: "turn",
	}
	winner, won, _, _, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, passiveRole)
	require.NoError(t, err)
	require.True(t, won)
	require.True(t, winner.Related)
	require.Equal(t, "system_passive", winner.PassiveFeature)
	require.NoError(t, StoreCodexTurnRootBinding(userID, turnID, rootID, rootBinding, passiveRole))

	mapping, _, found, err := ResolveCodexTurnRootBinding(context.Background(), userID, turnID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, mapping.Related)
	require.Equal(t, "system_passive", mapping.PassiveFeature)
}

func TestCodexThreadRootBindingResolvesChildAndSeparatesRoutingSides(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const (
		userID   = 46005
		rootA    = "01a06010-0000-7000-8000-000000000041"
		rootB    = "01a06010-0000-7000-8000-000000000042"
		threadID = "01a06010-0000-7000-8000-000000000043"
	)
	normal := CodexRootChannelBinding{ChannelID: 907, SelectedGroup: "pro", KeyFingerprint: "normal-key"}
	uaOnly := CodexRootChannelBinding{ChannelID: 908, SelectedGroup: "pro", KeyFingerprint: "ua-key", UARoutingOnly: true}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootA, normal))
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootB, uaOnly))
	require.NoError(t, StoreCodexThreadRootBinding(userID, threadID, rootA, normal))

	mapping, binding, found, err := ResolveCodexThreadRootBinding(context.Background(), userID, threadID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, rootA, mapping.RootID)
	require.Equal(t, normal, binding)

	require.NoError(t, StoreCodexThreadRootBinding(userID, threadID, rootB, uaOnly))
	_, _, found, err = ResolveCodexThreadRootBinding(context.Background(), userID, threadID)
	require.False(t, found)
	require.ErrorIs(t, err, ErrCodexTurnRootBindingConflict)
}

func TestReleaseProvisionalCodexLineageBindingsOnlyDeletesExactUnpromotedValue(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const (
		userID   = 46006
		rootID   = "01a06010-0000-7000-8000-000000000051"
		turnID   = "01a06010-0000-7000-8000-000000000052"
		threadID = "01a06010-0000-7000-8000-000000000053"
	)
	rootBinding := CodexRootChannelBinding{
		ChannelID: 909, SelectedGroup: "pro", KeyFingerprint: "release-key",
	}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, rootBinding))
	rootRole := CodexTurnRouteIdentity{RootOwner: true, ThreadSource: "user", RequestKind: "turn"}

	turnWinner, won, created, turnRollbackToken, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
	require.NoError(t, err)
	require.True(t, won)
	require.True(t, created)
	released, err := ReleaseProvisionalCodexTurnRootBinding(userID, turnID, turnWinner, turnRollbackToken)
	require.NoError(t, err)
	require.True(t, released)
	_, found, err := LoadCodexTurnRootBindingContext(context.Background(), userID, turnID)
	require.NoError(t, err)
	require.False(t, found)

	threadWinner, won, created, threadRollbackToken, err := ClaimProvisionalCodexThreadRootBinding(userID, threadID, rootID, rootBinding)
	require.NoError(t, err)
	require.True(t, won)
	require.True(t, created)
	wrongExpected := threadWinner
	wrongExpected.RootID = "01a06010-0000-7000-8000-000000000054"
	released, err = ReleaseProvisionalCodexThreadRootBinding(userID, threadID, wrongExpected, threadRollbackToken)
	require.NoError(t, err)
	require.False(t, released)
	released, err = ReleaseProvisionalCodexThreadRootBinding(userID, threadID, threadWinner, threadRollbackToken)
	require.NoError(t, err)
	require.True(t, released)
	_, found, err = LoadCodexThreadRootBindingContext(context.Background(), userID, threadID)
	require.NoError(t, err)
	require.False(t, found)
}

func TestReleaseProvisionalCodexLineageBindingsNeverDeletesPromotedValue(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const (
		userID   = 46007
		rootID   = "01a06010-0000-7000-8000-000000000061"
		turnID   = "01a06010-0000-7000-8000-000000000062"
		threadID = "01a06010-0000-7000-8000-000000000063"
	)
	rootBinding := CodexRootChannelBinding{
		ChannelID: 910, SelectedGroup: "pro", KeyFingerprint: "durable-release-key",
	}
	require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, rootBinding))
	rootRole := CodexTurnRouteIdentity{RootOwner: true, Related: true, ThreadSource: "user", RequestKind: "compaction"}

	turnWinner, won, created, turnRollbackToken, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
	require.NoError(t, err)
	require.True(t, won)
	require.True(t, created)
	require.NoError(t, StoreCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole))
	released, err := ReleaseProvisionalCodexTurnRootBinding(userID, turnID, turnWinner, turnRollbackToken)
	require.NoError(t, err)
	require.False(t, released)
	turnMapping, found, err := LoadCodexTurnRootBindingContext(context.Background(), userID, turnID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, turnWinner, turnMapping)

	threadWinner, won, created, threadRollbackToken, err := ClaimProvisionalCodexThreadRootBinding(userID, threadID, rootID, rootBinding)
	require.NoError(t, err)
	require.True(t, won)
	require.True(t, created)
	require.NoError(t, StoreCodexThreadRootBinding(userID, threadID, rootID, rootBinding))
	released, err = ReleaseProvisionalCodexThreadRootBinding(userID, threadID, threadWinner, threadRollbackToken)
	require.NoError(t, err)
	require.False(t, released)
	threadMapping, found, err := LoadCodexThreadRootBindingContext(context.Background(), userID, threadID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, threadWinner, threadMapping)
}

func TestProvisionalCodexLineageRollbackOwnershipIsInvalidatedBySameValueClaimant(t *testing.T) {
	testCases := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "redis", setup: func(t *testing.T) { useCodexPassiveRouteRedis(t) }},
		{name: "memory", setup: useCodexLineageMemory},
	}
	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.setup(t)
			userID := 46010 + index
			rootID := "01a06010-0000-7000-8000-000000000071"
			turnID := "01a06010-0000-7000-8000-000000000072"
			threadID := "01a06010-0000-7000-8000-000000000073"
			rootBinding := CodexRootChannelBinding{
				ChannelID: 911 + index, SelectedGroup: "pro", KeyFingerprint: "shared-claim-key",
			}
			require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, rootBinding))
			rootRole := CodexTurnRouteIdentity{RootOwner: true, ThreadSource: "user", RequestKind: "turn"}

			turnWinner, won, created, rollbackToken, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
			require.NoError(t, err)
			require.True(t, won)
			require.True(t, created)
			require.NotEmpty(t, rollbackToken)
			joinedWinner, joined, joinedCreated, joinedRollbackToken, err := ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, rootBinding, rootRole)
			require.NoError(t, err)
			require.True(t, joined)
			require.False(t, joinedCreated)
			require.Empty(t, joinedRollbackToken)
			require.Equal(t, turnWinner, joinedWinner)
			released, err := ReleaseProvisionalCodexTurnRootBinding(userID, turnID, turnWinner, rollbackToken)
			require.NoError(t, err)
			require.False(t, released)
			turnMapping, found, err := LoadCodexTurnRootBindingContext(context.Background(), userID, turnID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, turnWinner, turnMapping)

			threadWinner, won, created, rollbackToken, err := ClaimProvisionalCodexThreadRootBinding(userID, threadID, rootID, rootBinding)
			require.NoError(t, err)
			require.True(t, won)
			require.True(t, created)
			require.NotEmpty(t, rollbackToken)
			joinedThreadWinner, joined, joinedCreated, joinedRollbackToken, err := ClaimProvisionalCodexThreadRootBinding(userID, threadID, rootID, rootBinding)
			require.NoError(t, err)
			require.True(t, joined)
			require.False(t, joinedCreated)
			require.Empty(t, joinedRollbackToken)
			require.Equal(t, threadWinner, joinedThreadWinner)
			released, err = ReleaseProvisionalCodexThreadRootBinding(userID, threadID, threadWinner, rollbackToken)
			require.NoError(t, err)
			require.False(t, released)
			threadMapping, found, err := LoadCodexThreadRootBindingContext(context.Background(), userID, threadID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, threadWinner, threadMapping)
		})
	}
}
