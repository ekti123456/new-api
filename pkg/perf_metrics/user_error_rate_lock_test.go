package perfmetrics

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	configsetting "github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserErrorRateLockUsesRollingSamplesAndResetsAfterExpiry(t *testing.T) {
	configureUserErrorRateLockTest(t, false)
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(42)
	user.AccessURL = "https://chat.example.com"

	for i := 0; i < 50; i++ {
		assert.False(t, ObserveUserErrorRate(successfulUserErrorRateRelay(), user).Locked)
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked, "exactly 50 percent must not lock")

	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.True(t, status.Triggered)
	assert.Equal(t, int64(100), status.RequestCount)
	assert.Equal(t, int64(51), status.ErrorCount)
	assert.InDelta(t, 51, status.ErrorRate, 0.001)
	assert.Equal(t, int64(60), status.RetryAfter)
	assert.Equal(t, "https://chat.example.com", status.AccessURL)

	for i := 0; i < 100; i++ {
		status = ObserveUserErrorRate(failedUserErrorRateRelay(), user)
		assert.True(t, status.Locked)
		assert.False(t, status.Triggered)
	}
	now = now.Add(61 * time.Second)
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked, "the first request after expiry must start a fresh round")
}

func TestUserErrorRateLockMemoryKeepsSparseSamplesBeyondDashboardWindow(t *testing.T) {
	configureUserErrorRateLockTest(t, false)
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(47)

	for i := 0; i < 50; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	now = now.Add(3 * time.Hour)
	for i := 0; i < 49; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.Equal(t, int64(100), status.RequestCount)
	assert.Equal(t, int64(100), status.ErrorCount)
}

func TestUserErrorRateLockOnlyStartsForAnomalyEligibleUsers(t *testing.T) {
	setting := configureUserErrorRateLockTest(t, false)
	setting.UserErrorRateLockMinRequests = 2
	user := &UserMetricIdentity{UserID: 52}

	for i := 0; i < 10; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	userErrorRateMemory.Lock()
	_, tracked := userErrorRateMemory.states[user.UserID]
	userErrorRateMemory.Unlock()
	assert.False(t, tracked, "ordinary users must not allocate rolling lock samples")

	refreshUserErrorRateEligibility([]UserAnomalyItem{{UserID: user.UserID}})
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	require.True(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
}

func TestUserErrorRateLockIneligibleUserCreatesNoRedisState(t *testing.T) {
	mini := miniredis.RunT(t)
	configureUserErrorRateLockTest(t, true)
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })

	user := &UserMetricIdentity{UserID: 54}
	for i := 0; i < 10; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	assert.Empty(t, mini.Keys(), "ordinary users must not create Redis rolling-sample keys")
}

func TestUserErrorRateEligibilityRefreshExtendsGrace(t *testing.T) {
	configureUserErrorRateLockTest(t, false)
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	const userID = 55

	refreshUserErrorRateEligibility([]UserAnomalyItem{{UserID: userID}})
	firstDeadline, eligible := getUserErrorRateEligibilityDeadline(userID)
	require.True(t, eligible)
	now = now.Add(23 * time.Hour)
	refreshUserErrorRateEligibility([]UserAnomalyItem{{UserID: userID}})
	secondDeadline, eligible := getUserErrorRateEligibilityDeadline(userID)
	require.True(t, eligible)
	assert.Greater(t, secondDeadline, firstDeadline)
	now = now.Add(2 * time.Hour)
	_, eligible = getUserErrorRateEligibilityDeadline(userID)
	assert.True(t, eligible, "reappearing in the anomaly table must renew the 24-hour grace")
}

func TestUserErrorRateEligibilityExpiresAfter24HoursAndClearsSamples(t *testing.T) {
	setting := configureUserErrorRateLockTest(t, false)
	setting.UserErrorRateLockMinRequests = 3
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(53)

	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	now = now.Add(23*time.Hour + 59*time.Minute)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked, "the 24-hour grace must remain active")
	now = now.Add(2 * time.Minute)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked, "the first request after grace expiry must be ignored and clear old samples")

	refreshUserErrorRateEligibility([]UserAnomalyItem{{UserID: user.UserID}})
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	require.True(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked, "re-entry must start a fresh sample window")
}

func TestUserErrorRateEligibilityExpiryClearsRedisSamples(t *testing.T) {
	mini := miniredis.RunT(t)
	setting := configureUserErrorRateLockTest(t, true)
	setting.UserErrorRateLockMinRequests = 3
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(56)

	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	assert.NotEmpty(t, mini.Keys())
	mini.FastForward(24*time.Hour + time.Second)
	now = now.Add(24*time.Hour + time.Second)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	assert.Empty(t, mini.Keys(), "eligibility expiry must remove rolling samples before their fallback TTL")
}

func TestUserErrorRateLockMemoryRetainsNewestSamplesWhenCapacityChanges(t *testing.T) {
	setting := configureUserErrorRateLockTest(t, false)
	setting.UserErrorRateLockMinRequests = 4
	setting.UserErrorRateLockThreshold = 50
	user := eligibleUserErrorRateTest(48)

	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	assert.False(t, ObserveUserErrorRate(successfulUserErrorRateRelay(), user).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	assert.False(t, ObserveUserErrorRate(successfulUserErrorRateRelay(), user).Locked)

	setting.UserErrorRateLockMinRequests = 3
	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked, "resizing must retain the newest samples before appending")
	assert.Equal(t, int64(3), status.RequestCount)
	assert.Equal(t, int64(2), status.ErrorCount)
}

func TestUserErrorRateLockRequiresMinimumSamples(t *testing.T) {
	configureUserErrorRateLockTest(t, false)
	user := eligibleUserErrorRateTest(45)

	for i := 0; i < 99; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	require.True(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
}

func TestUserErrorRateLockIgnoresDisabledUnmonitoredAndExcludedSamples(t *testing.T) {
	setting := configureUserErrorRateLockTest(t, false)
	user := eligibleUserErrorRateTest(43)

	setting.UserErrorRateLockEnabled = false
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)

	setting.UserErrorRateLockEnabled = true
	unmonitored := failedUserErrorRateRelay()
	unmonitored.UsingGroup = "other"
	for i := 0; i < 101; i++ {
		assert.False(t, ObserveUserErrorRate(unmonitored, user).Locked)
	}

	excluded := failedUserErrorRateRelay()
	excluded.ExcludeFromPerformanceMetrics = true
	for i := 0; i < 101; i++ {
		assert.False(t, ObserveUserErrorRate(excluded, user).Locked)
	}
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
}

func TestUserErrorRateLockRedisIsSharedAndResetsAfterTTL(t *testing.T) {
	mini := miniredis.RunT(t)
	setting := configureUserErrorRateLockTest(t, true)
	setting.UserErrorRateLockMinRequests = 2
	setting.UserErrorRateLockThreshold = 50
	setting.UserErrorRateLockSeconds = 60
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })

	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(44)
	user.AccessURL = "https://cf-chat.example.com"

	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.True(t, status.Triggered)
	assert.Equal(t, int64(2), status.RequestCount)
	assert.Equal(t, "https://cf-chat.example.com", status.AccessURL)
	lookupStatus := GetUserErrorRateLock(user.UserID)
	assert.True(t, lookupStatus.Locked)
	assert.False(t, lookupStatus.Triggered)
	assert.Equal(t, "https://cf-chat.example.com", GetUserErrorRateLock(user.UserID).AccessURL)

	mini.FastForward(61 * time.Second)
	now = now.Add(61 * time.Second)
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
}

func TestUserErrorRateLockRedisKeepsSparseSamplesBeyondDashboardWindow(t *testing.T) {
	mini := miniredis.RunT(t)
	setting := configureUserErrorRateLockTest(t, true)
	setting.UserErrorRateLockMinRequests = 3
	setting.UserErrorRateLockThreshold = 50
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })

	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(49)

	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	mini.FastForward(3 * time.Hour)
	now = now.Add(3 * time.Hour)
	assert.False(t, ObserveUserErrorRate(successfulUserErrorRateRelay(), user).Locked)
	mini.FastForward(3 * time.Hour)
	now = now.Add(3 * time.Hour)
	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.Equal(t, int64(3), status.RequestCount)
	assert.Equal(t, int64(2), status.ErrorCount)
}

func TestUserErrorRateLockRedisRollsOutOldestSamples(t *testing.T) {
	mini := miniredis.RunT(t)
	setting := configureUserErrorRateLockTest(t, true)
	setting.UserErrorRateLockMinRequests = 4
	setting.UserErrorRateLockThreshold = 50
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })
	user := eligibleUserErrorRateTest(50)

	for _, failed := range []bool{true, true, false, false, false, true, true} {
		info := successfulUserErrorRateRelay()
		if failed {
			info = failedUserErrorRateRelay()
		}
		assert.False(t, ObserveUserErrorRate(info, user).Locked)
	}
	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.Equal(t, int64(4), status.RequestCount)
	assert.Equal(t, int64(3), status.ErrorCount)
}

func TestUserErrorRateLockTriggerClearsAllGroupSamples(t *testing.T) {
	mini := miniredis.RunT(t)
	setting := configureUserErrorRateLockTest(t, true)
	setting.UserAnomalyMonitoredGroups = []string{"pro", "other"}
	setting.UserErrorRateLockMinRequests = 3
	setting.UserErrorRateLockThreshold = 50
	setting.UserErrorRateLockSeconds = 1
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := eligibleUserErrorRateTest(51)

	otherFailure := failedUserErrorRateRelay()
	otherFailure.UsingGroup = "other"
	assert.False(t, ObserveUserErrorRate(otherFailure, user).Locked)
	for i := 0; i < 2; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	require.True(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)

	mini.FastForward(2 * time.Second)
	now = now.Add(2 * time.Second)
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	assert.False(t, ObserveUserErrorRate(otherFailure, user).Locked)
	assert.False(t, ObserveUserErrorRate(otherFailure, user).Locked, "the pre-lock sample from another group must have been cleared")
}

func TestUserErrorRateLockRedisCountsConcurrentCompletionsAtomically(t *testing.T) {
	mini := miniredis.RunT(t)
	configureUserErrorRateLockTest(t, true)
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })
	user := eligibleUserErrorRateTest(46)

	var waitGroup sync.WaitGroup
	for i := 0; i < 100; i++ {
		waitGroup.Add(1)
		isError := i < 51
		go func() {
			defer waitGroup.Done()
			if isError {
				ObserveUserErrorRate(failedUserErrorRateRelay(), user)
				return
			}
			ObserveUserErrorRate(successfulUserErrorRateRelay(), user)
		}()
	}
	waitGroup.Wait()

	status := GetUserErrorRateLock(user.UserID)
	require.True(t, status.Locked)
	assert.Equal(t, int64(100), status.RequestCount)
	assert.Equal(t, int64(51), status.ErrorCount)
}

func configureUserErrorRateLockTest(t *testing.T, redisEnabled bool) *perf_metrics_setting.PerfMetricsSetting {
	t.Helper()
	setting := configsetting.GlobalConfig.Get("perf_metrics_setting").(*perf_metrics_setting.PerfMetricsSetting)
	originalSetting := *setting
	originalRedisEnabled := common.RedisEnabled
	originalRedis := common.RDB
	originalNow := userErrorRateNow
	userErrorRateMemory.Lock()
	originalStates := userErrorRateMemory.states
	originalLocks := userErrorRateMemory.locks
	userErrorRateMemory.states = make(map[int]map[string]*userErrorRateCounter)
	userErrorRateMemory.locks = make(map[int]userErrorRateLock)
	userErrorRateMemory.Unlock()
	userErrorRateEligibility.Lock()
	originalEligibilityDeadlines := userErrorRateEligibility.deadlines
	userErrorRateEligibility.deadlines = make(map[int]int64)
	userErrorRateEligibility.Unlock()

	*setting = perf_metrics_setting.PerfMetricsSetting{
		Enabled:                      true,
		UserAnomalyMonitoredGroups:   []string{"pro"},
		UserErrorRateLockEnabled:     true,
		UserErrorRateLockMinRequests: 100,
		UserErrorRateLockThreshold:   50,
		UserErrorRateLockSeconds:     60,
	}
	common.RedisEnabled = redisEnabled
	common.RDB = nil
	t.Cleanup(func() {
		*setting = originalSetting
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRedis
		userErrorRateNow = originalNow
		userErrorRateMemory.Lock()
		userErrorRateMemory.states = originalStates
		userErrorRateMemory.locks = originalLocks
		userErrorRateMemory.Unlock()
		userErrorRateEligibility.Lock()
		userErrorRateEligibility.deadlines = originalEligibilityDeadlines
		userErrorRateEligibility.Unlock()
	})
	return setting
}

func eligibleUserErrorRateTest(userID int) *UserMetricIdentity {
	refreshUserErrorRateEligibility([]UserAnomalyItem{{UserID: userID}})
	return &UserMetricIdentity{UserID: userID}
}

func successfulUserErrorRateRelay() *relaycommon.RelayInfo {
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	return &relaycommon.RelayInfo{IsStream: true, StreamStatus: status, UsingGroup: "pro"}
}

func failedUserErrorRateRelay() *relaycommon.RelayInfo {
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonTimeout, assert.AnError)
	return &relaycommon.RelayInfo{IsStream: true, StreamStatus: status, UsingGroup: "pro"}
}
