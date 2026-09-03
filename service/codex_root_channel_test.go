package service

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func useCodexPassiveRouteRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	codexRootChannelCacheOnce = sync.Once{}
	codexRootChannelCache = nil
	codexLegacyRootChannelCacheOnce = sync.Once{}
	codexLegacyRootChannelCache = nil
	codexTurnRootCacheOnce = sync.Once{}
	codexTurnRootCache = nil
	codexTurnRootMemoryExpiresAt = make(map[string]time.Time)
	codexTurnRootRollbackOwners = make(map[string]codexLineageRollbackOwner)
	codexTurnRootWaiters.Lock()
	codexTurnRootWaiters.items = make(map[string]*codexRootChannelWaiter)
	codexTurnRootWaiters.Unlock()
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
		codexRootChannelCacheOnce = sync.Once{}
		codexRootChannelCache = nil
		codexLegacyRootChannelCacheOnce = sync.Once{}
		codexLegacyRootChannelCache = nil
		codexTurnRootCacheOnce = sync.Once{}
		codexTurnRootCache = nil
		codexTurnRootMemoryExpiresAt = make(map[string]time.Time)
		codexTurnRootRollbackOwners = make(map[string]codexLineageRollbackOwner)
		codexTurnRootWaiters.Lock()
		codexTurnRootWaiters.items = make(map[string]*codexRootChannelWaiter)
		codexTurnRootWaiters.Unlock()
		require.NoError(t, client.Close())
	})
	return server
}

func codexPassiveRootAliasForBinding(rootID string, binding CodexRootChannelBinding) CodexPassiveRootAlias {
	return CodexPassiveRootAlias{
		RootID:             rootID,
		SelectedGroup:      binding.SelectedGroup,
		UARoutingOnly:      binding.UARoutingOnly,
		BindingFingerprint: CodexRootChannelBindingFingerprint(binding),
	}
}

func TestCodexRootChannelBindingRoundtripIsScopedByUserAndRoot(t *testing.T) {
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{
		ChannelID:      731,
		SelectedGroup:  "pro",
		KeyIndex:       2,
		KeyFingerprint: "0123456789abcdef",
		UARoutingOnly:  true,
	}
	require.NoError(t, StoreCodexRootChannelBinding(42, rootID, binding))

	got, found, err := LoadCodexRootChannelBinding(42, rootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, binding, got)

	_, found, err = LoadCodexRootChannelBinding(43, rootID)
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = LoadCodexRootChannelBinding(42, rootID+"-other")
	require.NoError(t, err)
	require.False(t, found)
}

func TestCodexRootChannelBindingUpdateWakesExactRootAndLoadsAcrossRoutingSides(t *testing.T) {
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
	})

	const userID = 42101
	targetRootID := "target:" + t.Name()
	otherRootID := "other:" + t.Name()
	binding := CodexRootChannelBinding{
		ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "target-key", UARoutingOnly: true,
	}
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- WaitForCodexRootChannelBindingUpdate(context.Background(), userID, targetRootID, time.Second)
	}()

	targetWaiterKey := legacyCodexRootChannelCacheKey(userID, targetRootID)
	require.Eventually(t, func() bool {
		codexRootChannelWaiters.Lock()
		defer codexRootChannelWaiters.Unlock()
		return codexRootChannelWaiters.items[targetWaiterKey] != nil
	}, time.Second, time.Millisecond)

	require.NoError(t, StoreProvisionalCodexRootChannelBinding(userID, otherRootID, binding))
	codexRootChannelWaiters.Lock()
	targetWaiter := codexRootChannelWaiters.items[targetWaiterKey]
	codexRootChannelWaiters.Unlock()
	require.NotNil(t, targetWaiter, "publishing another root must not wake the target waiter")

	require.NoError(t, StoreProvisionalCodexRootChannelBinding(userID, targetRootID, binding))
	require.NoError(t, <-waitResult)
	got, found, err := LoadCodexRootChannelBindingContext(context.Background(), userID, targetRootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, binding, got)
}

func TestWaitForCodexRootChannelBindingUpdateHonorsCancellation(t *testing.T) {
	const userID = 42102
	rootID := "root:" + t.Name()
	ctx, cancel := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- WaitForCodexRootChannelBindingUpdate(ctx, userID, rootID, time.Second)
	}()

	waiterKey := legacyCodexRootChannelCacheKey(userID, rootID)
	require.Eventually(t, func() bool {
		codexRootChannelWaiters.Lock()
		defer codexRootChannelWaiters.Unlock()
		return codexRootChannelWaiters.items[waiterKey] != nil
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-waitResult, context.Canceled)
	codexRootChannelWaiters.Lock()
	cleaned := codexRootChannelWaiters.items[waiterKey] == nil
	codexRootChannelWaiters.Unlock()
	require.True(t, cleaned)
}

func TestCodexRootChannelBindingRejectsIncompleteValues(t *testing.T) {
	rootID := "root:" + t.Name()
	require.NoError(t, StoreCodexRootChannelBinding(42, rootID, CodexRootChannelBinding{ChannelID: 731}))
	_, found, err := LoadCodexRootChannelBinding(42, rootID)
	require.NoError(t, err)
	require.False(t, found)
}

func TestCodexProvisionalRootBindingDoesNotReplaceExistingDurableBinding(t *testing.T) {
	rootID := "root:" + t.Name()
	durable := CodexRootChannelBinding{ChannelID: 731, SelectedGroup: "pro", KeyFingerprint: "durable-key"}
	provisional := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "provisional-key"}
	require.NoError(t, StoreCodexRootChannelBinding(42, rootID, durable))
	require.NoError(t, StoreProvisionalCodexRootChannelBinding(42, rootID, provisional))

	got, found, err := LoadCodexRootChannelBinding(42, rootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, durable, got)
}

func TestCodexRecentRootChannelBindingIsScopedByUserAndToken(t *testing.T) {
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{
		ChannelID:      812,
		SelectedGroup:  "pro",
		KeyIndex:       1,
		KeyFingerprint: "abcdef0123456789",
	}
	require.NoError(t, StoreRecentCodexRootChannelBinding(42001, 10101, rootID, binding))

	got, err := LoadRecentCodexRootChannelCandidates(context.Background(), 42001, 10101, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, rootID, got[0].RootID)
	require.Equal(t, binding, got[0].Binding)

	got, err = LoadRecentCodexRootChannelCandidates(context.Background(), 42002, 10101, false)
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = LoadRecentCodexRootChannelCandidates(context.Background(), 42001, 10102, false)
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = LoadRecentCodexRootChannelCandidates(context.Background(), 42001, 10101, true)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestCodexRecentRootChannelBindingRejectsMissingToken(t *testing.T) {
	rootID := "root:" + t.Name()
	require.NoError(t, StoreRecentCodexRootChannelBinding(42, 0, rootID, CodexRootChannelBinding{
		ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789",
	}))
	got, err := LoadRecentCodexRootChannelCandidates(context.Background(), 42, 0, false)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestCodexRecentRootChannelCandidatesPreserveConcurrentRoots(t *testing.T) {
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(42003, 10103, "root-a:"+t.Name(), binding))
	require.NoError(t, StoreRecentCodexRootChannelBinding(42003, 10103, "root-b:"+t.Name(), binding))

	got, err := LoadRecentCodexRootChannelCandidates(context.Background(), 42003, 10103, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestCodexRecentRootCandidateOnlyStoreRequiresExactCurrentBinding(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42020, 10120
	rootID := "root:" + t.Name()
	current := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	mismatch := CodexRootChannelBinding{ChannelID: 813, SelectedGroup: "pro", KeyFingerprint: "key-b"}
	_, _, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, current)
	require.NoError(t, err)

	require.ErrorIs(t,
		StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, mismatch),
		ErrCodexRecentRootBindingUnavailable,
	)
	candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Empty(t, candidates)

	require.NoError(t, StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, current))
	candidates, err = LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, CodexRootChannelBindingFingerprint(current), candidates[0].BindingFingerprint)
}

func TestCodexProvisionalRecentRootBindingDoesNotLeakWinnerIntoLosingTokenScope(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, losingTokenID = 42021, 10121
	rootID := "root:" + t.Name()
	winner := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	loser := CodexRootChannelBinding{ChannelID: 813, SelectedGroup: "pro", KeyFingerprint: "key-b"}
	_, selectedWon, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, winner)
	require.NoError(t, err)
	require.True(t, selectedWon)

	require.ErrorIs(t,
		StoreProvisionalRecentCodexRootChannelBinding(userID, losingTokenID, rootID, loser),
		ErrCodexRootChannelBindingConflict,
	)
	candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, losingTokenID, false)
	require.NoError(t, err)
	require.Empty(t, candidates)
}

func TestCodexPassiveRootAliasIsImmutableAndScoped(t *testing.T) {
	const userID, tokenID = 42004, 10104
	rootID := "root-a:" + t.Name()
	systemRootID := "system:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	alias := codexPassiveRootAliasForBinding(rootID, binding)
	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
	require.NoError(t, PromoteCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))

	got, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, alias, got)
	_, found, err = LoadCodexPassiveRootAlias(context.Background(), userID, tokenID+1, systemRootID)
	require.NoError(t, err)
	require.False(t, found)

	conflicting := alias
	conflicting.RootID = "root-b:" + t.Name()
	require.ErrorIs(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, conflicting), ErrCodexPassiveRootAliasConflict)
	got, found, err = LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, alias, got)
}

func TestCodexTitleRootCandidatesMergeRoutingSidesAndRequireFreshRecentIntersection(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42201, 10201
	normalRoot := "normal:" + t.Name()
	uaRoot := "ua:" + t.Name()
	normal := CodexRootChannelBinding{ChannelID: 821, SelectedGroup: "pro", KeyFingerprint: "normal-key"}
	ua := CodexRootChannelBinding{ChannelID: 822, SelectedGroup: "pro", KeyFingerprint: "ua-key", UARoutingOnly: true}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, normalRoot, normal))
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, uaRoot, ua))
	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, normalRoot, normal))
	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, uaRoot, ua))

	candidates, err := LoadCodexTitleRootChannelCandidates(context.Background(), userID, tokenID)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	byRoot := map[string]CodexRootChannelBinding{}
	for _, candidate := range candidates {
		byRoot[candidate.RootID] = candidate.Binding
	}
	require.Equal(t, normal, byRoot[normalRoot])
	require.Equal(t, ua, byRoot[uaRoot])

	err = ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, "title:"+t.Name(), codexPassiveRootAliasForBinding(normalRoot, normal))
	require.ErrorIs(t, err, ErrCodexPassiveRootCandidatesChanged, "two candidates across different sides must remain ambiguous")
}

func TestCodexTitleRootCandidateExpiresBeforeRecentCandidate(t *testing.T) {
	server := useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42202, 10202
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 823, SelectedGroup: "pro", KeyFingerprint: "title-key"}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding))

	candidates, err := LoadCodexTitleRootChannelCandidates(context.Background(), userID, tokenID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	server.FastForward(codexTitleRootCandidateTTL + time.Second)
	candidates, err = LoadCodexTitleRootChannelCandidates(context.Background(), userID, tokenID)
	require.NoError(t, err)
	require.Empty(t, candidates)
	require.ErrorIs(t,
		ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, "title:"+t.Name(), codexPassiveRootAliasForBinding(rootID, binding)),
		ErrCodexPassiveRootCandidatesChanged,
	)
}

func TestCodexTitleRootAliasClaimIsUniqueInRedis(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42203, 10203
	rootID := "root:" + t.Name()
	titleID := "title:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 824, SelectedGroup: "pro", KeyFingerprint: "winner-key", UARoutingOnly: true}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding))
	alias := codexPassiveRootAliasForBinding(rootID, binding)
	require.NoError(t, ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, titleID, alias))
	require.NoError(t, ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, titleID, alias))
	conflicting := alias
	conflicting.SelectedGroup = "other"
	require.ErrorIs(t, ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, titleID, conflicting), ErrCodexPassiveRootCandidatesChanged)
	got, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, titleID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, alias, got)
}

func TestCodexTitleRootAliasMemoryClaimRejectsCrossSideAmbiguity(t *testing.T) {
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
	})
	const userID, tokenID = 42204, 10204
	normalRoot := "normal:" + t.Name()
	uaRoot := "ua:" + t.Name()
	normal := CodexRootChannelBinding{ChannelID: 825, SelectedGroup: "pro", KeyFingerprint: "normal-memory"}
	ua := CodexRootChannelBinding{ChannelID: 826, SelectedGroup: "pro", KeyFingerprint: "ua-memory", UARoutingOnly: true}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, normalRoot, normal))
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, uaRoot, ua))
	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, normalRoot, normal))
	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, uaRoot, ua))
	require.ErrorIs(t,
		ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, "title:"+t.Name(), codexPassiveRootAliasForBinding(normalRoot, normal)),
		ErrCodexPassiveRootCandidatesChanged,
	)
}

func TestCodexTitleRootCandidateMemoryStoreNotifiesWaiterAndClaimsNormalSide(t *testing.T) {
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
	})
	const userID, tokenID = 42205, 10205
	rootID := "root:" + t.Name()
	titleID := "title:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 827, SelectedGroup: "pro", KeyFingerprint: "normal-notify"}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- WaitForCodexTitleRootChannelUpdate(context.Background(), userID, tokenID, time.Second)
	}()
	waiterScope := codexPassiveRootRedisScopeKey(userID, tokenID)
	require.Eventually(t, func() bool {
		codexTitleRootWaiters.Lock()
		defer codexTitleRootWaiters.Unlock()
		return codexTitleRootWaiters.items[waiterScope] != nil
	}, time.Second, time.Millisecond)

	require.NoError(t, StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding))
	require.NoError(t, <-waitResult)
	alias := codexPassiveRootAliasForBinding(rootID, binding)
	require.NoError(t, ClaimCodexTitleRootAlias(context.Background(), userID, tokenID, titleID, alias))
	got, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, titleID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, alias, got)
}

func TestCodexPassiveRootAliasClaimRejectsAmbiguousCandidates(t *testing.T) {
	const userID, tokenID = 42005, 10105
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, "root-a:"+t.Name(), binding))
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, "root-b:"+t.Name(), binding))

	err := ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, "system:"+t.Name(), CodexPassiveRootAlias{
		RootID: "root-a:" + t.Name(), SelectedGroup: "pro", BindingFingerprint: CodexRootChannelBindingFingerprint(binding),
	})
	require.Error(t, err)
	_, found, loadErr := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, "system:"+t.Name())
	require.NoError(t, loadErr)
	require.False(t, found)
}

func TestCodexRootObservationCutoffPreservesSameRootRequestHistory(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			var server *miniredis.Miniredis
			if backend == "redis" {
				server = useCodexPassiveRouteRedis(t)
				server.SetTime(time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC))
			} else {
				originalEnabled := common.RedisEnabled
				originalClient := common.RDB
				common.RedisEnabled = false
				common.RDB = nil
				t.Cleanup(func() {
					common.RedisEnabled = originalEnabled
					common.RDB = originalClient
				})
			}

			const userID, tokenID = 42015, 10115
			rootID := "root:" + t.Name()
			systemRootID := "system:" + t.Name()
			binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
			require.NoError(t, StoreCodexRootChannelBinding(userID, rootID, binding))

			arrivalA, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
			require.NoError(t, err)
			require.NoError(t, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrivalA))
			// Provisional and success publication for A reuse one exact event.
			require.NoError(t, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrivalA))

			if server != nil {
				server.SetTime(arrivalA.ArrivedAt.Add(time.Second))
			}
			cutoffB, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
			require.NoError(t, err)
			if server != nil {
				server.SetTime(cutoffB.ArrivedAt.Add(time.Second))
			}
			arrivalC, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
			require.NoError(t, err)
			require.NoError(t, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrivalC))

			candidate, found, err := LoadLatestCodexRootChannelObservationBefore(
				context.Background(), userID, tokenID, false, cutoffB,
			)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, arrivalA.Order, candidate.ArrivalOrder)
			require.Equal(t, rootID, candidate.RootID)
			require.NotEqual(t, arrivalC.Order, candidate.ArrivalOrder)

			alias := codexPassiveRootAliasForBinding(rootID, binding)
			require.NoError(t, ClaimCodexObservedPassiveRootAlias(
				context.Background(), userID, tokenID, systemRootID, alias, candidate, cutoffB,
			))
			stored, aliasFound, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
			require.NoError(t, err)
			require.True(t, aliasFound)
			require.Equal(t, alias, stored)
		})
	}
}

func TestCodexPassiveRootAliasClaimRequiresExactCandidateFingerprint(t *testing.T) {
	const userID, tokenID = 42012, 10112
	rootID := "root:" + t.Name()
	systemRootID := "system:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))

	missingFingerprint := codexPassiveRootAliasForBinding(rootID, binding)
	missingFingerprint.BindingFingerprint = ""
	require.ErrorIs(t,
		ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, missingFingerprint),
		ErrCodexPassiveRootAliasInvalid,
	)

	wrongFingerprint := codexPassiveRootAliasForBinding(rootID, binding)
	wrongFingerprint.BindingFingerprint = strings.Repeat("0", len(wrongFingerprint.BindingFingerprint))
	require.ErrorIs(t,
		ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, wrongFingerprint),
		ErrCodexPassiveRootCandidatesChanged,
	)

	_, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
	require.NoError(t, err)
	require.False(t, found)
}

func TestCodexRecentRootCandidateRedisUsesProvisionalAndSuccessfulLifetimes(t *testing.T) {
	t.Run("provisional outlives successful window", func(t *testing.T) {
		server := useCodexPassiveRouteRedis(t)
		redisNow := time.Now().UTC()
		server.SetTime(redisNow)
		const userID, tokenID = 42013, 10113
		rootID := "root:" + t.Name()
		binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
		require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))

		candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		require.WithinDuration(t, time.Now().Add(codexProvisionalRootCandidateTTL), candidates[0].ExpiresAt, 2*time.Second)
		candidateKey := codexRecentRootChannelRedisKey(codexRecentRootChannelScopeKey(userID, tokenID, false))
		require.Greater(t, server.TTL(candidateKey), codexRecentRootChannelCandidateTTL)

		advance := codexRecentRootChannelCandidateTTL + time.Second
		server.FastForward(advance)
		redisNow = redisNow.Add(advance)
		server.SetTime(redisNow)
		exists, err := common.RDB.Exists(context.Background(), candidateKey).Result()
		require.NoError(t, err)
		require.Equal(t, int64(1), exists, "the shared container must not expire with the 30-second successful window")
		candidates, err = LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Len(t, candidates, 1)

		advance = codexProvisionalRootCandidateTTL - codexRecentRootChannelCandidateTTL
		server.FastForward(advance)
		redisNow = redisNow.Add(advance)
		server.SetTime(redisNow)
		candidates, err = LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Empty(t, candidates)
	})

	t.Run("successful expires while shared container remains", func(t *testing.T) {
		server := useCodexPassiveRouteRedis(t)
		redisNow := time.Now().UTC()
		server.SetTime(redisNow)
		const userID, tokenID = 42014, 10114
		rootID := "root:" + t.Name()
		binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
		require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
		candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		require.WithinDuration(t, time.Now().Add(codexRecentRootChannelCandidateTTL), candidates[0].ExpiresAt, 2*time.Second)

		candidateKey := codexRecentRootChannelRedisKey(codexRecentRootChannelScopeKey(userID, tokenID, false))
		require.Greater(t, server.TTL(candidateKey), codexRecentRootChannelCandidateTTL)
		advance := codexRecentRootChannelCandidateTTL + time.Second
		server.FastForward(advance)
		server.SetTime(redisNow.Add(advance))
		exists, err := common.RDB.Exists(context.Background(), candidateKey).Result()
		require.NoError(t, err)
		require.Equal(t, int64(1), exists)
		candidates, err = LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Empty(t, candidates)
	})

	t.Run("successful settlement replaces provisional expiry", func(t *testing.T) {
		server := useCodexPassiveRouteRedis(t)
		redisNow := time.Now().UTC()
		server.SetTime(redisNow)
		const userID, tokenID = 42022, 10122
		rootID := "root:" + t.Name()
		binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
		require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
		candidateKey := codexRecentRootChannelRedisKey(codexRecentRootChannelScopeKey(userID, tokenID, false))
		member := codexRecentRootCandidateMember(rootID, CodexRootChannelBindingFingerprint(binding))
		provisionalExpiry, err := common.RDB.ZScore(context.Background(), candidateKey, member).Result()
		require.NoError(t, err)

		require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
		successfulExpiry, err := common.RDB.ZScore(context.Background(), candidateKey, member).Result()
		require.NoError(t, err)
		require.Less(t, successfulExpiry, provisionalExpiry)
		require.InDelta(t, time.Now().Add(codexRecentRootChannelCandidateTTL).UnixMilli(), successfulExpiry, float64(2*time.Second/time.Millisecond))

		advance := codexRecentRootChannelCandidateTTL + time.Second
		server.FastForward(advance)
		server.SetTime(redisNow.Add(advance))
		candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Empty(t, candidates)
	})

	t.Run("new provisional activity extends successful expiry", func(t *testing.T) {
		server := useCodexPassiveRouteRedis(t)
		redisNow := time.Now().UTC()
		server.SetTime(redisNow)
		const userID, tokenID = 42028, 10128
		rootID := "root:" + t.Name()
		binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
		require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
		candidateKey := codexRecentRootChannelRedisKey(codexRecentRootChannelScopeKey(userID, tokenID, false))
		member := codexRecentRootCandidateMember(rootID, CodexRootChannelBindingFingerprint(binding))
		successfulExpiry, err := common.RDB.ZScore(context.Background(), candidateKey, member).Result()
		require.NoError(t, err)

		server.FastForward(time.Second)
		redisNow = redisNow.Add(time.Second)
		server.SetTime(redisNow)
		require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
		provisionalExpiry, err := common.RDB.ZScore(context.Background(), candidateKey, member).Result()
		require.NoError(t, err)
		require.Greater(t, provisionalExpiry, successfulExpiry)

		advance := codexRecentRootChannelCandidateTTL + time.Second
		server.FastForward(advance)
		server.SetTime(redisNow.Add(advance))
		candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
	})
}

func TestCodexRecentRootCandidateMemoryUsesProvisionalAndSuccessfulLifetimes(t *testing.T) {
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
	})

	const userID, tokenID = 42029, 10129
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	provisionalExpiry := candidates[0].ExpiresAt
	require.WithinDuration(t, time.Now().Add(codexProvisionalRootCandidateTTL), provisionalExpiry, 2*time.Second)

	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	candidates, err = LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	successfulExpiry := candidates[0].ExpiresAt
	require.WithinDuration(t, time.Now().Add(codexRecentRootChannelCandidateTTL), successfulExpiry, 2*time.Second)
	require.True(t, successfulExpiry.Before(provisionalExpiry))

	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	candidates, err = LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.WithinDuration(t, time.Now().Add(codexProvisionalRootCandidateTTL), candidates[0].ExpiresAt, 2*time.Second)
	require.True(t, candidates[0].ExpiresAt.After(successfulExpiry))
}

func TestCodexPassiveRootAliasRedisClaimRejectsExpiredOrReboundCandidate(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		useCodexPassiveRouteRedis(t)
		const userID, tokenID = 42018, 10118
		rootID := "root:" + t.Name()
		systemRootID := "system:" + t.Name()
		binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
		require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, false)
		candidateKey := codexRecentRootChannelRedisKey(scopeKey)
		member := codexRecentRootCandidateMember(rootID, CodexRootChannelBindingFingerprint(binding))
		require.NoError(t, common.RDB.ZAdd(context.Background(), candidateKey, &redis.Z{
			Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: member,
		}).Err())

		err := ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, codexPassiveRootAliasForBinding(rootID, binding))
		require.ErrorIs(t, err, ErrCodexPassiveRootCandidatesChanged)
		_, found, loadErr := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
		require.NoError(t, loadErr)
		require.False(t, found)
	})

	t.Run("rebound", func(t *testing.T) {
		useCodexPassiveRouteRedis(t)
		const userID, tokenID = 42019, 10119
		rootID := "root:" + t.Name()
		systemRootID := "system:" + t.Name()
		original := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
		require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, original))

		rebound := CodexRootChannelBinding{ChannelID: 813, SelectedGroup: "pro", KeyFingerprint: "key-b"}
		reboundPayload, err := common.Marshal(rebound)
		require.NoError(t, err)
		rootKey := getCodexRootChannelCache().FullKey(codexRootChannelCacheKey(userID, rootID, false))
		require.NoError(t, common.RDB.Set(context.Background(), rootKey, reboundPayload, codexProvisionalRootChannelCacheTTL).Err())

		err = ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, codexPassiveRootAliasForBinding(rootID, original))
		require.ErrorIs(t, err, ErrCodexPassiveRootCandidatesChanged)
		err = ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, codexPassiveRootAliasForBinding(rootID, rebound))
		require.ErrorIs(t, err, ErrCodexPassiveRootCandidatesChanged)
		_, found, loadErr := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
		require.NoError(t, loadErr)
		require.False(t, found)
	})
}

func TestCodexRecentRootLoaderRemovesStaleRedisMemberBeforeAliasClaim(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42023, 10123
	rootID := "root-valid:" + t.Name()
	systemRootID := "system:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))

	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, false)
	candidateKey := codexRecentRootChannelRedisKey(scopeKey)
	staleMember := codexRecentRootCandidateMember("root-stale:"+t.Name(), strings.Repeat("f", 64))
	staleExpiry := time.Now().Add(codexRecentRootChannelCandidateTTL).UnixMilli()
	require.NoError(t, common.RDB.ZAdd(context.Background(), candidateKey, &redis.Z{
		Score: float64(staleExpiry), Member: staleMember,
	}).Err())

	candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, rootID, candidates[0].RootID)
	_, err = common.RDB.ZScore(context.Background(), candidateKey, staleMember).Result()
	require.ErrorIs(t, err, redis.Nil)

	alias := codexPassiveRootAliasForBinding(rootID, binding)
	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
}

func TestCodexRecentRootLoaderIgnoresLegacyReadErrorWhenV2BindingExists(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42026, 10126
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	legacyKey := getCodexLegacyRootChannelCache().FullKey(legacyCodexRootChannelCacheKey(userID, rootID))
	require.NoError(t, common.RDB.RPush(context.Background(), legacyKey, "wrong-type").Err())

	candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, binding, candidates[0].Binding)
}

func TestCodexRecentRootStaleCleanupPreservesConcurrentlyRefreshedMember(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42024, 10124
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, false)
	candidateKey := codexRecentRootChannelRedisKey(scopeKey)
	member := codexRecentRootCandidateMember("root:"+t.Name(), strings.Repeat("a", 64))
	oldExpiry := time.Now().Add(time.Minute).UnixMilli()
	refreshedExpiry := oldExpiry + int64(time.Minute/time.Millisecond)
	require.NoError(t, common.RDB.ZAdd(context.Background(), candidateKey, &redis.Z{
		Score: float64(refreshedExpiry), Member: member,
	}).Err())

	require.NoError(t, removeStaleCodexRecentRootCandidates(context.Background(), scopeKey, map[string]int64{
		member: oldExpiry,
	}))
	storedExpiry, err := common.RDB.ZScore(context.Background(), candidateKey, member).Result()
	require.NoError(t, err)
	require.Equal(t, float64(refreshedExpiry), storedExpiry)
}

func TestCodexPassiveRootAliasRedisKeysShareClusterHashTag(t *testing.T) {
	const userID, tokenID = 42015, 10115
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, false)
	redisScopeKey := codexPassiveRootRedisScopeKey(userID, tokenID)
	candidateKey := codexRecentRootChannelRedisKey(scopeKey)
	aliasKey := codexPassiveRootAliasRedisKey(redisScopeKey, codexPassiveRootAliasCacheKey(userID, tokenID, "system:"+t.Name()))

	hashTag := func(key string) string {
		start := strings.IndexByte(key, '{')
		if start < 0 {
			return ""
		}
		end := strings.IndexByte(key[start+1:], '}')
		if end <= 0 {
			return ""
		}
		return key[start+1 : start+1+end]
	}
	require.NotEqual(t, candidateKey, aliasKey)
	require.Equal(t, redisScopeKey, hashTag(candidateKey))
	require.Equal(t, redisScopeKey, hashTag(aliasKey))
}

func TestLoadCodexPassiveRootAliasRejectsMissingBindingFingerprint(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42016, 10116
	systemRootID := "system:" + t.Name()
	cacheKey := codexPassiveRootAliasCacheKey(userID, tokenID, systemRootID)
	redisKey := codexPassiveRootAliasRedisKey(codexPassiveRootRedisScopeKey(userID, tokenID), cacheKey)
	payload, err := common.Marshal(CodexPassiveRootAlias{RootID: "root:" + t.Name(), SelectedGroup: "pro"})
	require.NoError(t, err)
	require.NoError(t, common.RDB.Set(context.Background(), redisKey, payload, time.Minute).Err())

	_, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
	require.ErrorIs(t, err, ErrCodexPassiveRootAliasInvalid)
	require.False(t, found)
}

func TestCodexPassiveRootAliasOperationsHonorCanceledContextInMemory(t *testing.T) {
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
	})
	const userID, tokenID = 42025, 10125
	rootID := "root:" + t.Name()
	systemRootID := "system:" + t.Name() + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	alias := codexPassiveRootAliasForBinding(rootID, binding)
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t,
		ClaimCodexPassiveRootAlias(canceledContext, userID, tokenID, systemRootID, alias),
		context.Canceled,
	)
	_, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
	require.ErrorIs(t,
		PromoteCodexPassiveRootAlias(canceledContext, userID, tokenID, systemRootID, alias),
		context.Canceled,
	)
	_, found, err = LoadCodexPassiveRootAlias(canceledContext, userID, tokenID, systemRootID)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, found)
	loaded, found, err := LoadCodexPassiveRootAlias(nil, userID, tokenID, systemRootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, alias, loaded)
}

func TestCodexRecentRootCandidatesDoNotLoseConcurrentRedisWriters(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42006, 10106
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789"}
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	var workers sync.WaitGroup
	for _, rootID := range []string{"root-a:" + t.Name(), "root-b:" + t.Name()} {
		rootID := rootID
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errorsChannel <- StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding)
		}()
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		require.NoError(t, err)
	}

	got, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestCodexPassiveRootAliasRedisClaimHasOneWinner(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42007, 10107
	rootID := "root-a:" + t.Name()
	systemRootID := "system:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789"}
	require.NoError(t, StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	aliases := []CodexPassiveRootAlias{
		codexPassiveRootAliasForBinding(rootID, binding),
		{RootID: rootID, SelectedGroup: "other", BindingFingerprint: CodexRootChannelBindingFingerprint(binding)},
	}
	start := make(chan struct{})
	results := make(chan error, len(aliases))
	var workers sync.WaitGroup
	for _, alias := range aliases {
		alias := alias
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	stored, found, err := LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, aliases, stored)
}

func TestCodexPassiveRootAliasRedisPromotionExtendsTTL(t *testing.T) {
	server := useCodexPassiveRouteRedis(t)
	require.Greater(t, codexProvisionalRootChannelCacheTTL, codexProvisionalRootCandidateTTL)
	const userID, tokenID = 42010, 10110
	rootID := "root:" + t.Name()
	systemRootID := "system:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	alias := codexPassiveRootAliasForBinding(rootID, binding)
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, binding))
	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
	aliasKey := codexPassiveRootAliasRedisKey(codexPassiveRootRedisScopeKey(userID, tokenID), codexPassiveRootAliasCacheKey(userID, tokenID, systemRootID))
	rootKey := getCodexRootChannelCache().FullKey(codexRootChannelCacheKey(userID, rootID, binding.UARoutingOnly))
	require.LessOrEqual(t, server.TTL(aliasKey), codexPassiveRootAliasProvisionalTTL)
	require.LessOrEqual(t, server.TTL(rootKey), codexProvisionalRootChannelCacheTTL)
	require.Greater(t, server.TTL(rootKey), server.TTL(aliasKey), "the exact root binding must outlive every provisional passive reference")
	require.NoError(t, PromoteCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
	require.Greater(t, server.TTL(aliasKey), 23*time.Hour)
	require.Greater(t, server.TTL(rootKey), 23*time.Hour)
	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))
	require.Greater(t, server.TTL(aliasKey), 23*time.Hour, "an idempotent concurrent claim must not shorten a durable alias")
	require.Greater(t, server.TTL(rootKey), 23*time.Hour, "an idempotent concurrent claim must not shorten the durable root binding")
}

func TestCodexPassiveRootAliasPromotionRejectsReboundRootBinding(t *testing.T) {
	server := useCodexPassiveRouteRedis(t)
	const userID, tokenID = 42017, 10117
	rootID := "root:" + t.Name()
	systemRootID := "system:" + t.Name()
	original := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(t, StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID, rootID, original))
	alias := codexPassiveRootAliasForBinding(rootID, original)
	require.NoError(t, ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias))

	rebound := CodexRootChannelBinding{ChannelID: 813, SelectedGroup: "pro", KeyFingerprint: "key-b"}
	reboundPayload, err := common.Marshal(rebound)
	require.NoError(t, err)
	rootKey := getCodexRootChannelCache().FullKey(codexRootChannelCacheKey(userID, rootID, false))
	require.NoError(t, common.RDB.Set(context.Background(), rootKey, reboundPayload, codexProvisionalRootChannelCacheTTL).Err())

	err = ClaimCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias)
	require.ErrorIs(t, err, ErrCodexPassiveRootCandidatesChanged)
	err = PromoteCodexPassiveRootAlias(context.Background(), userID, tokenID, systemRootID, alias)
	require.Error(t, err)
	aliasKey := codexPassiveRootAliasRedisKey(codexPassiveRootRedisScopeKey(userID, tokenID), codexPassiveRootAliasCacheKey(userID, tokenID, systemRootID))
	require.LessOrEqual(t, server.TTL(aliasKey), codexPassiveRootAliasProvisionalTTL)
	require.LessOrEqual(t, server.TTL(rootKey), codexProvisionalRootChannelCacheTTL)
}

func TestCodexRootChannelBindingRedisClaimHasOneWinner(t *testing.T) {
	useCodexPassiveRouteRedis(t)
	const userID = 42008
	rootID := "root:" + t.Name()
	bindings := []CodexRootChannelBinding{
		{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"},
		{ChannelID: 813, SelectedGroup: "pro", KeyFingerprint: "key-b"},
	}
	type claimResult struct {
		winner CodexRootChannelBinding
		won    bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan claimResult, len(bindings))
	var workers sync.WaitGroup
	for _, binding := range bindings {
		binding := binding
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			winner, won, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
			results <- claimResult{winner: winner, won: won, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	claims := make([]claimResult, 0, len(bindings))
	for result := range results {
		require.NoError(t, result.err)
		claims = append(claims, result)
	}
	require.Len(t, claims, 2)
	require.Equal(t, claims[0].winner, claims[1].winner)
	winnerCount := 0
	for _, result := range claims {
		if result.won {
			winnerCount++
		}
	}
	require.Equal(t, 1, winnerCount)
	loser := bindings[0]
	if loser == claims[0].winner {
		loser = bindings[1]
	}
	require.ErrorIs(t, StoreCodexRootChannelBinding(userID, rootID, loser), ErrCodexRootChannelBindingConflict)
	stored, found, err := LoadCodexRootChannelBinding(userID, rootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, claims[0].winner, stored)
}

func TestCodexIdempotentProvisionalClaimExtendsOnlyShortExactBinding(t *testing.T) {
	server := useCodexPassiveRouteRedis(t)
	const userID = 42026
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}

	provisionalRoot := "root-provisional:" + t.Name()
	_, won, err := ClaimProvisionalCodexRootChannelBinding(userID, provisionalRoot, binding)
	require.NoError(t, err)
	require.True(t, won)
	server.FastForward(2*time.Minute + 30*time.Second)
	_, won, err = ClaimProvisionalCodexRootChannelBinding(userID, provisionalRoot, binding)
	require.NoError(t, err)
	require.True(t, won)
	provisionalKey := getCodexRootChannelCache().FullKey(codexRootChannelCacheKey(userID, provisionalRoot, false))
	require.Greater(t, server.TTL(provisionalKey), 2*time.Minute+50*time.Second)

	durableRoot := "root-durable:" + t.Name()
	require.NoError(t, StoreCodexRootChannelBinding(userID, durableRoot, binding))
	durableKey := getCodexRootChannelCache().FullKey(codexRootChannelCacheKey(userID, durableRoot, false))
	durableTTL := server.TTL(durableKey)
	_, won, err = ClaimProvisionalCodexRootChannelBinding(userID, durableRoot, binding)
	require.NoError(t, err)
	require.True(t, won)
	require.GreaterOrEqual(t, server.TTL(durableKey), durableTTL-time.Second, "an idempotent claim must not shorten a durable root")
}

func TestCodexIdempotentProvisionalClaimExtendsTrackedMemoryBinding(t *testing.T) {
	originalEnabled := common.RedisEnabled
	originalClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalEnabled
		common.RDB = originalClient
	})
	const userID = 42027
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	_, won, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
	require.NoError(t, err)
	require.True(t, won)
	key := codexRootChannelCacheKey(userID, rootID, false)

	codexRootChannelWriteMu.Lock()
	codexRootChannelMemoryExpiresAt[key] = time.Now().Add(time.Second)
	codexRootChannelWriteMu.Unlock()
	_, won, err = ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
	require.NoError(t, err)
	require.True(t, won)
	codexRootChannelWriteMu.Lock()
	trackedExpiry := codexRootChannelMemoryExpiresAt[key]
	codexRootChannelWriteMu.Unlock()
	require.WithinDuration(t, time.Now().Add(codexProvisionalRootChannelCacheTTL), trackedExpiry, 2*time.Second)
}

func TestCodexRootChannelBindingClaimsAreIsolatedByUARoutingSide(t *testing.T) {
	const userID = 42009
	rootID := "root:" + t.Name()
	normal := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-normal"}
	uaOnly := CodexRootChannelBinding{ChannelID: 813, SelectedGroup: "pro", KeyFingerprint: "key-ua", UARoutingOnly: true}

	normalWinner, normalWon, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, normal)
	require.NoError(t, err)
	require.True(t, normalWon)
	require.Equal(t, normal, normalWinner)
	uaWinner, uaWon, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, uaOnly)
	require.NoError(t, err)
	require.True(t, uaWon)
	require.Equal(t, uaOnly, uaWinner)

	storedNormal, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, false)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, normal, storedNormal)
	storedUA, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, true)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uaOnly, storedUA)
}

func TestCodexRootChannelBindingReadsMatchingLegacyRoutingSide(t *testing.T) {
	const userID = 42011
	rootID := "root:" + t.Name()
	legacy := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "legacy-key", UARoutingOnly: true}
	require.NoError(t, getCodexLegacyRootChannelCache().SetWithTTL(legacyCodexRootChannelCacheKey(userID, rootID), legacy, time.Hour))

	_, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, false)
	require.NoError(t, err)
	require.False(t, found)
	got, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, true)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, legacy, got)
}
