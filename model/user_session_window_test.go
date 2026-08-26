package model

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func resetUserSessionWindowRuntimeForTest() {
	userSessionWindowRuntime.Lock()
	userSessionWindowRuntime.items = make(map[int]map[string]UserSessionWindowStatus)
	userSessionWindowRuntime.lastPersisted = make(map[int]map[string]time.Time)
	userSessionWindowRuntime.lastCleanup = time.Time{}
	userSessionWindowRuntime.Unlock()
}

func TestUpdateUserSessionWindowFromHeader(t *testing.T) {
	header := http.Header{}
	header.Set("X-Codex2API-Session-Limit", "5")
	header.Set("X-Codex2API-Session-Used", "3")
	header.Set("X-Codex2API-Session-Window-Seconds", "3600")
	UpdateUserSessionWindowFromHeader(12345, "https://codex-a.example", header)
	status, ok := GetUserSessionWindowStatus(12345)
	if !ok || status.Used != 3 || status.Limit != 5 || status.WindowSeconds != 3600 {
		t.Fatalf("unexpected session window status: ok=%v status=%+v", ok, status)
	}
}

func TestUserSessionWindowAggregatesDistinctTargets(t *testing.T) {
	userID := 12346
	first := http.Header{}
	first.Set("X-Codex2API-Session-Limit", "5")
	first.Set("X-Codex2API-Session-Used", "3")
	first.Set("X-Codex2API-Session-Window-Seconds", "3600")
	second := http.Header{}
	second.Set("X-Codex2API-Session-Limit", "4")
	second.Set("X-Codex2API-Session-Used", "1")
	second.Set("X-Codex2API-Session-Window-Seconds", "1800")
	UpdateUserSessionWindowFromHeader(userID, "https://codex-a.example", first)
	UpdateUserSessionWindowFromHeader(userID, "https://codex-b.example", second)
	status, ok := GetUserSessionWindowStatus(userID)
	if !ok || status.Used != 4 || status.Limit != 9 || status.WindowSeconds != 3600 {
		t.Fatalf("unexpected aggregate: ok=%v status=%+v", ok, status)
	}
}

func TestListFullUserSessionWindowTargetsDoesNotUseAggregate(t *testing.T) {
	userID := 12347
	full := http.Header{}
	full.Set("X-Codex2API-Session-Limit", "5")
	full.Set("X-Codex2API-Session-Used", "5")
	full.Set("X-Codex2API-Session-Window-Seconds", "3600")
	available := http.Header{}
	available.Set("X-Codex2API-Session-Limit", "5")
	available.Set("X-Codex2API-Session-Used", "0")
	available.Set("X-Codex2API-Session-Window-Seconds", "3600")
	UpdateUserSessionWindowFromHeader(userID, "https://codex-full.example", full)
	UpdateUserSessionWindowFromHeader(userID, "https://codex-available.example", available)

	aggregated, ok := GetUserSessionWindowStatus(userID)
	if !ok || aggregated.Used != 5 || aggregated.Limit != 10 {
		t.Fatalf("unexpected aggregate: ok=%v status=%+v", ok, aggregated)
	}
	items := ListUserSessionWindowTargetStatuses(true)
	matched := make([]UserSessionWindowTargetStatus, 0, 1)
	for _, item := range items {
		if item.UserID == userID {
			matched = append(matched, item)
		}
	}
	if len(matched) != 1 || matched[0].Target != "https://codex-full.example" || !matched[0].Full {
		t.Fatalf("full target list=%+v, want only codex-full", matched)
	}
}

func TestUserSessionWindowRestoresFromRedisAfterRuntimeRestart(t *testing.T) {
	server := miniredis.RunT(t)
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB = previousRedisEnabled, previousRDB
		resetUserSessionWindowRuntimeForTest()
	})
	resetUserSessionWindowRuntimeForTest()

	header := http.Header{}
	header.Set("X-Codex2API-Session-Limit", "5")
	header.Set("X-Codex2API-Session-Used", "4")
	header.Set("X-Codex2API-Session-Window-Seconds", "4800")
	UpdateUserSessionWindowFromHeader(22345, "https://codex.example", header)

	resetUserSessionWindowRuntimeForTest()
	targets := ListUserSessionWindowTargetStatuses(false)
	require.Len(t, targets, 1)
	require.Equal(t, 22345, targets[0].UserID)
	require.Equal(t, "https://codex.example", targets[0].Target)

	resetUserSessionWindowRuntimeForTest()
	status, ok := GetUserSessionWindowStatus(22345)
	require.True(t, ok)
	require.Equal(t, 4, status.Used)
	require.Equal(t, 5, status.Limit)
	require.Equal(t, 4800, status.WindowSeconds)
}

func TestUserSessionWindowRedisRestoreDropsExpiredTargets(t *testing.T) {
	server := miniredis.RunT(t)
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB = previousRedisEnabled, previousRDB
		resetUserSessionWindowRuntimeForTest()
	})
	resetUserSessionWindowRuntimeForTest()

	status := UserSessionWindowStatus{Used: 1, Limit: 5, WindowSeconds: 60, UpdatedAt: time.Now().Add(-2 * time.Minute)}
	payload, err := common.Marshal(status)
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(context.Background(), userSessionWindowRedisDataKey(22346), "https://expired.example", payload).Err())
	globalPayload, err := common.Marshal(persistedUserSessionWindowTarget{UserID: 22346, Target: "https://expired.example", UserSessionWindowStatus: status})
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(context.Background(), userSessionWindowRedisAllKey, userSessionWindowRedisField(22346, "https://expired.example"), globalPayload).Err())

	_, ok := GetUserSessionWindowStatus(22346)
	require.False(t, ok)
	require.Zero(t, common.RDB.HLen(context.Background(), userSessionWindowRedisDataKey(22346)).Val())
}

func TestUserSessionWindowGlobalRestoreDropsExpiredPersonalTarget(t *testing.T) {
	server := miniredis.RunT(t)
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB = previousRedisEnabled, previousRDB
		resetUserSessionWindowRuntimeForTest()
	})
	resetUserSessionWindowRuntimeForTest()

	const userID = 22347
	const target = "https://expired-global.example"
	status := UserSessionWindowStatus{Used: 5, Limit: 5, WindowSeconds: 60, UpdatedAt: time.Now().Add(-2 * time.Minute)}
	payload, err := common.Marshal(status)
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(context.Background(), userSessionWindowRedisDataKey(userID), target, payload).Err())
	globalPayload, err := common.Marshal(persistedUserSessionWindowTarget{UserID: userID, Target: target, UserSessionWindowStatus: status})
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(context.Background(), userSessionWindowRedisAllKey, userSessionWindowRedisField(userID, target), globalPayload).Err())

	require.Empty(t, ListUserSessionWindowTargetStatuses(false))
	require.Zero(t, common.RDB.HLen(context.Background(), userSessionWindowRedisAllKey).Val())
	require.Zero(t, common.RDB.HLen(context.Background(), userSessionWindowRedisDataKey(userID)).Val())
}

func TestUserSessionWindowRedisWriteIsThrottledUntilValueChanges(t *testing.T) {
	server := miniredis.RunT(t)
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled, common.RDB = previousRedisEnabled, previousRDB
		resetUserSessionWindowRuntimeForTest()
	})
	resetUserSessionWindowRuntimeForTest()

	const userID = 22348
	const target = "https://codex-throttle.example"
	header := http.Header{}
	header.Set("X-Codex2API-Session-Limit", "5")
	header.Set("X-Codex2API-Session-Used", "1")
	header.Set("X-Codex2API-Session-Window-Seconds", "3600")
	UpdateUserSessionWindowFromHeader(userID, target, header)
	first := common.RDB.HGet(context.Background(), userSessionWindowRedisDataKey(userID), target).Val()
	require.NotEmpty(t, first)

	UpdateUserSessionWindowFromHeader(userID, target, header)
	second := common.RDB.HGet(context.Background(), userSessionWindowRedisDataKey(userID), target).Val()
	require.Equal(t, first, second, "unchanged status should not rewrite Redis on every request")

	header.Set("X-Codex2API-Session-Used", "2")
	UpdateUserSessionWindowFromHeader(userID, target, header)
	third := common.RDB.HGet(context.Background(), userSessionWindowRedisDataKey(userID), target).Val()
	require.NotEqual(t, second, third, "changed status must be persisted immediately")
}
