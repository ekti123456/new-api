package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexRootObservationSurvivesElevenMinuteRequestGap(test *testing.T) {
	server := useCodexPassiveRouteRedis(test)
	base := time.Now().UTC()
	server.SetTime(base)
	const userID, tokenID = 43001, 10301
	rootID := "root:" + test.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(test, StoreCodexRootChannelBinding(userID, rootID, binding))
	first, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
	require.NoError(test, err)
	require.NoError(test, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, first))

	server.FastForward(11 * time.Minute)
	server.SetTime(base.Add(11 * time.Minute))
	next, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
	require.NoError(test, err)
	assert.Greater(test, next.Order, first.Order)
	require.NoError(test, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, next))
}

func TestCodexRootObservationRefreshPreservesPendingRequestsAndRejectsConflicts(test *testing.T) {
	server := useCodexPassiveRouteRedis(test)
	server.SetTime(time.Now().UTC())
	const userID, tokenID = 43003, 10303
	rootID := "root:" + test.Name()
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a"}
	require.NoError(test, StoreCodexRootChannelBinding(userID, rootID, binding))
	first, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
	require.NoError(test, err)
	require.NoError(test, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, first))
	pending, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
	require.NoError(test, err)
	require.NoError(test, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, first))
	require.NoError(test, StoreCodexRootChannelBinding(userID, rootID+"-other", binding))
	assert.ErrorIs(test, StoreCodexRootChannelObservation(userID, tokenID, rootID+"-other", binding, first), ErrCodexPassiveRootCandidatesChanged)

	next, err := BeginCodexRequestArrival(context.Background(), userID, tokenID)
	require.NoError(test, err)
	assert.Greater(test, next.Order, pending.Order)
	candidate, found, err := LoadLatestCodexRootChannelObservationBefore(context.Background(), userID, tokenID, false, next)
	require.NoError(test, err)
	require.True(test, found)
	assert.Equal(test, first.Order, candidate.ArrivalOrder)
	assert.Equal(test, rootID, candidate.RootID)
}

func TestCodexArrivalRecoversSequenceFromBothRoutingSides(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		for _, counterState := range []string{"missing", "behind"} {
			test.Run(backend+"/"+counterState, func(test *testing.T) {
				if backend == "redis" {
					useCodexPassiveRouteRedis(test)
				} else {
					originalEnabled, originalClient := common.RedisEnabled, common.RDB
					common.RedisEnabled, common.RDB = false, nil
					test.Cleanup(func() { common.RedisEnabled, common.RDB = originalEnabled, originalClient })
				}
				const userID, tokenID = 43002, 10302
				scope := CodexPassiveRootScope{UserID: userID, TokenID: tokenID, InstallationID: test.Name()}
				var last CodexRequestArrival
				for _, uaRoutingOnly := range []bool{false, true} {
					binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "key-a", UARoutingOnly: uaRoutingOnly}
					rootID := "root:" + test.Name()
					require.NoError(test, StoreCodexRootChannelBinding(userID, rootID, binding))
					arrival, err := BeginCodexRequestArrival(context.Background(), userID, tokenID, scope)
					require.NoError(test, err)
					require.NoError(test, StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrival, scope))
					last = arrival
				}
				scopeKey := codexPassiveRootScopeKey(userID, tokenID, scope)
				if backend == "redis" {
					key := codexRequestArrivalSequenceRedisKey(scopeKey)
					if counterState == "missing" {
						require.NoError(test, common.RDB.Del(context.Background(), key).Err())
					} else {
						require.NoError(test, common.RDB.Set(context.Background(), key, 1, time.Minute).Err())
					}
				} else if counterState == "missing" {
					getCodexRequestArrivalMemory().Delete(scopeKey)
				} else {
					getCodexRequestArrivalMemory().SetWithTTL(scopeKey, 1, time.Minute)
				}

				next, err := BeginCodexRequestArrival(context.Background(), userID, tokenID, scope)
				require.NoError(test, err)
				assert.Greater(test, next.Order, last.Order)
			})
		}
	}
}
