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

func TestUserErrorRateLockThresholdAndExpiryReset(t *testing.T) {
	configureUserErrorRateLockTest(t, false)
	now := time.Unix(1_800_000_000, 0)
	userErrorRateNow = func() time.Time { return now }
	user := &UserMetricIdentity{UserID: 42}

	for i := 0; i < 50; i++ {
		assert.False(t, ObserveUserErrorRate(successfulUserErrorRateRelay(), user).Locked)
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked, "exactly 50 percent must not lock")

	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.Equal(t, int64(101), status.RequestCount)
	assert.Equal(t, int64(51), status.ErrorCount)
	assert.InDelta(t, 50.495, status.ErrorRate, 0.001)
	assert.Equal(t, int64(60), status.RetryAfter)

	for i := 0; i < 100; i++ {
		assert.True(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	now = now.Add(61 * time.Second)
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked, "the first request after expiry must start a fresh round")
}

func TestUserErrorRateLockRequiresMinimumSamples(t *testing.T) {
	configureUserErrorRateLockTest(t, false)
	user := &UserMetricIdentity{UserID: 45}

	for i := 0; i < 99; i++ {
		assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	}
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	require.True(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
}

func TestUserErrorRateLockIgnoresDisabledUnmonitoredAndExcludedSamples(t *testing.T) {
	setting := configureUserErrorRateLockTest(t, false)
	user := &UserMetricIdentity{UserID: 43}

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
	user := &UserMetricIdentity{UserID: 44}

	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
	status := ObserveUserErrorRate(failedUserErrorRateRelay(), user)
	require.True(t, status.Locked)
	assert.Equal(t, int64(2), status.RequestCount)
	assert.True(t, GetUserErrorRateLock(user.UserID).Locked)

	mini.FastForward(61 * time.Second)
	now = now.Add(61 * time.Second)
	assert.False(t, GetUserErrorRateLock(user.UserID).Locked)
	assert.False(t, ObserveUserErrorRate(failedUserErrorRateRelay(), user).Locked)
}

func TestUserErrorRateLockRedisCountsConcurrentCompletionsAtomically(t *testing.T) {
	mini := miniredis.RunT(t)
	configureUserErrorRateLockTest(t, true)
	common.RDB = redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, common.RDB.Close()) })
	user := &UserMetricIdentity{UserID: 46}

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
	})
	return setting
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
