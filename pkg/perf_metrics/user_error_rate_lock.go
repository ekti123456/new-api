package perfmetrics

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/go-redis/redis/v8"
)

const (
	userErrorRateWindowSeconds = int64(perf_metrics_setting.UserAnomalyRetentionHours * 3600)
	maxUserErrorRateUsers      = 100000
	userErrorRateRedisTimeout  = 500 * time.Millisecond
)

type UserErrorRateLockStatus struct {
	Locked       bool
	Group        string
	RequestCount int64
	ErrorCount   int64
	ErrorRate    float64
	RetryAfter   int64
	LockSeconds  int
}

type userErrorRateCounter struct {
	WindowStartedAt int64
	RequestCount    int64
	ErrorCount      int64
}

type userErrorRateLock struct {
	Group        string
	RequestCount int64
	ErrorCount   int64
	LockedUntil  int64
	LockSeconds  int
}

var userErrorRateNow = time.Now

var userErrorRateMemory = struct {
	sync.Mutex
	states map[int]map[string]*userErrorRateCounter
	locks  map[int]userErrorRateLock
}{
	states: make(map[int]map[string]*userErrorRateCounter),
	locks:  make(map[int]userErrorRateLock),
}

var observeUserErrorRateScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return {0, 0, 0}
end

local now = tonumber(ARGV[1])
local window_seconds = tonumber(ARGV[2])
local started_at = tonumber(redis.call('HGET', KEYS[2], 'started_at'))
if not started_at or now - started_at >= window_seconds then
  redis.call('DEL', KEYS[2])
  started_at = now
  redis.call('HSET', KEYS[2], 'started_at', started_at)
end

local request_count = redis.call('HINCRBY', KEYS[2], 'request_count', 1)
local error_count = tonumber(redis.call('HGET', KEYS[2], 'error_count')) or 0
if tonumber(ARGV[3]) == 1 then
  error_count = redis.call('HINCRBY', KEYS[2], 'error_count', 1)
end
redis.call('SADD', KEYS[3], KEYS[2])
redis.call('EXPIRE', KEYS[2], window_seconds + 60)
redis.call('EXPIRE', KEYS[3], window_seconds + 60)

local min_requests = tonumber(ARGV[4])
local threshold_millionths = tonumber(ARGV[5])
if request_count >= min_requests and error_count * 100000000 > request_count * threshold_millionths then
  local lock_seconds = tonumber(ARGV[6])
  redis.call('HSET', KEYS[1],
    'group', ARGV[7],
    'request_count', request_count,
    'error_count', error_count,
    'locked_until', now + lock_seconds,
    'lock_seconds', lock_seconds)
  redis.call('EXPIRE', KEYS[1], lock_seconds)

  local state_keys = redis.call('SMEMBERS', KEYS[3])
  for _, state_key in ipairs(state_keys) do
    redis.call('DEL', state_key)
  end
  redis.call('DEL', KEYS[3])
  return {1, request_count, error_count}
end

return {0, request_count, error_count}
`)

func ObserveUserErrorRate(info *relaycommon.RelayInfo, user *UserMetricIdentity) UserErrorRateLockStatus {
	if !perf_metrics_setting.IsUserErrorRateLockEnabled() || info == nil || info.ExcludeFromPerformanceMetrics || user == nil || user.UserID <= 0 {
		return UserErrorRateLockStatus{}
	}
	group := info.UsingGroup
	if !perf_metrics_setting.IsUserAnomalyGroupMonitored(group) {
		return UserErrorRateLockStatus{}
	}

	isError := !IsRelaySampleSuccess(info)
	if common.RedisEnabled && common.RDB != nil {
		status, err := observeUserErrorRateRedis(user.UserID, group, isError)
		if err != nil {
			common.SysLog(fmt.Sprintf("user error-rate lock observation failed for user %d: %v", user.UserID, err))
			return UserErrorRateLockStatus{}
		}
		return status
	}
	return observeUserErrorRateMemory(user.UserID, group, isError)
}

func GetUserErrorRateLock(userID int) UserErrorRateLockStatus {
	if userID <= 0 || !perf_metrics_setting.IsUserErrorRateLockEnabled() {
		return UserErrorRateLockStatus{}
	}
	if common.RedisEnabled && common.RDB != nil {
		status, err := getUserErrorRateLockRedis(userID)
		if err != nil {
			common.SysLog(fmt.Sprintf("user error-rate lock lookup failed for user %d: %v", userID, err))
			return UserErrorRateLockStatus{}
		}
		return status
	}
	return getUserErrorRateLockMemory(userID)
}

func observeUserErrorRateMemory(userID int, group string, isError bool) UserErrorRateLockStatus {
	now := userErrorRateNow().Unix()
	userErrorRateMemory.Lock()
	defer userErrorRateMemory.Unlock()

	if lock, ok := userErrorRateMemory.locks[userID]; ok {
		if lock.LockedUntil > now {
			return buildUserErrorRateLockStatus(lock, now)
		}
		delete(userErrorRateMemory.locks, userID)
		delete(userErrorRateMemory.states, userID)
	}

	states := userErrorRateMemory.states[userID]
	if states == nil {
		if len(userErrorRateMemory.states)+len(userErrorRateMemory.locks) >= maxUserErrorRateUsers {
			purgeExpiredUserErrorRateStatesLocked(now)
			if len(userErrorRateMemory.states)+len(userErrorRateMemory.locks) >= maxUserErrorRateUsers {
				return UserErrorRateLockStatus{}
			}
		}
		states = make(map[string]*userErrorRateCounter)
		userErrorRateMemory.states[userID] = states
	}

	counter := states[group]
	if counter == nil || now-counter.WindowStartedAt >= userErrorRateWindowSeconds {
		counter = &userErrorRateCounter{WindowStartedAt: now}
		states[group] = counter
	}
	counter.RequestCount++
	if isError {
		counter.ErrorCount++
	}

	threshold := perf_metrics_setting.GetUserErrorRateLockThreshold()
	if counter.RequestCount < int64(perf_metrics_setting.GetUserErrorRateLockMinRequests()) || float64(counter.ErrorCount)*100 <= float64(counter.RequestCount)*threshold {
		return UserErrorRateLockStatus{}
	}

	lockSeconds := perf_metrics_setting.GetUserErrorRateLockSeconds()
	lock := userErrorRateLock{
		Group:        group,
		RequestCount: counter.RequestCount,
		ErrorCount:   counter.ErrorCount,
		LockedUntil:  now + int64(lockSeconds),
		LockSeconds:  lockSeconds,
	}
	delete(userErrorRateMemory.states, userID)
	userErrorRateMemory.locks[userID] = lock
	return buildUserErrorRateLockStatus(lock, now)
}

func getUserErrorRateLockMemory(userID int) UserErrorRateLockStatus {
	now := userErrorRateNow().Unix()
	userErrorRateMemory.Lock()
	defer userErrorRateMemory.Unlock()

	lock, ok := userErrorRateMemory.locks[userID]
	if !ok {
		return UserErrorRateLockStatus{}
	}
	if lock.LockedUntil <= now {
		delete(userErrorRateMemory.locks, userID)
		delete(userErrorRateMemory.states, userID)
		return UserErrorRateLockStatus{}
	}
	return buildUserErrorRateLockStatus(lock, now)
}

func purgeExpiredUserErrorRateStatesLocked(now int64) {
	for userID, lock := range userErrorRateMemory.locks {
		if lock.LockedUntil <= now {
			delete(userErrorRateMemory.locks, userID)
		}
	}
	for userID, states := range userErrorRateMemory.states {
		active := false
		for group, state := range states {
			if now-state.WindowStartedAt >= userErrorRateWindowSeconds {
				delete(states, group)
				continue
			}
			active = true
		}
		if !active {
			delete(userErrorRateMemory.states, userID)
		}
	}
}

func buildUserErrorRateLockStatus(lock userErrorRateLock, now int64) UserErrorRateLockStatus {
	retryAfter := lock.LockedUntil - now
	if retryAfter < 1 {
		retryAfter = 1
	}
	errorRate := 0.0
	if lock.RequestCount > 0 {
		errorRate = float64(lock.ErrorCount) * 100 / float64(lock.RequestCount)
	}
	return UserErrorRateLockStatus{
		Locked:       true,
		Group:        lock.Group,
		RequestCount: lock.RequestCount,
		ErrorCount:   lock.ErrorCount,
		ErrorRate:    errorRate,
		RetryAfter:   retryAfter,
		LockSeconds:  lock.LockSeconds,
	}
}

func observeUserErrorRateRedis(userID int, group string, isError bool) (UserErrorRateLockStatus, error) {
	lockKey, statesKey, stateKey := userErrorRateRedisKeys(userID, group)
	now := userErrorRateNow().Unix()
	errorValue := 0
	if isError {
		errorValue = 1
	}
	thresholdMillionths := int64(math.Round(perf_metrics_setting.GetUserErrorRateLockThreshold() * 1000000))
	lockSeconds := perf_metrics_setting.GetUserErrorRateLockSeconds()

	ctx, cancel := context.WithTimeout(context.Background(), userErrorRateRedisTimeout)
	defer cancel()
	result, err := observeUserErrorRateScript.Run(ctx, common.RDB, []string{lockKey, stateKey, statesKey},
		now,
		userErrorRateWindowSeconds,
		errorValue,
		perf_metrics_setting.GetUserErrorRateLockMinRequests(),
		thresholdMillionths,
		lockSeconds,
		group,
	).Slice()
	if err != nil {
		return UserErrorRateLockStatus{}, err
	}
	if len(result) < 3 || redisResultInt64(result[0]) != 1 {
		return UserErrorRateLockStatus{}, nil
	}
	requestCount := redisResultInt64(result[1])
	errorCount := redisResultInt64(result[2])
	return buildUserErrorRateLockStatus(userErrorRateLock{
		Group:        group,
		RequestCount: requestCount,
		ErrorCount:   errorCount,
		LockedUntil:  now + int64(lockSeconds),
		LockSeconds:  lockSeconds,
	}, now), nil
}

func getUserErrorRateLockRedis(userID int) (UserErrorRateLockStatus, error) {
	lockKey, _, _ := userErrorRateRedisKeys(userID, "")
	ctx, cancel := context.WithTimeout(context.Background(), userErrorRateRedisTimeout)
	defer cancel()
	pipeline := common.RDB.Pipeline()
	defer pipeline.Close()
	valuesCommand := pipeline.HGetAll(ctx, lockKey)
	ttlCommand := pipeline.TTL(ctx, lockKey)
	_, err := pipeline.Exec(ctx)
	if err != nil {
		return UserErrorRateLockStatus{}, err
	}
	values := valuesCommand.Val()
	if len(values) == 0 {
		return UserErrorRateLockStatus{}, nil
	}
	retryAfter := int64(math.Ceil(ttlCommand.Val().Seconds()))
	if retryAfter < 1 {
		return UserErrorRateLockStatus{}, nil
	}

	now := userErrorRateNow().Unix()
	lock := userErrorRateLock{
		Group:        values["group"],
		RequestCount: parseRedisLockInt(values["request_count"]),
		ErrorCount:   parseRedisLockInt(values["error_count"]),
		LockedUntil:  now + retryAfter,
		LockSeconds:  int(parseRedisLockInt(values["lock_seconds"])),
	}
	return buildUserErrorRateLockStatus(lock, now), nil
}

func userErrorRateRedisKeys(userID int, group string) (string, string, string) {
	tag := fmt.Sprintf("{%d}", userID)
	groupHash := sha256.Sum256([]byte(group))
	prefix := "perf:user-error-lock:v1:" + tag
	return prefix + ":lock", prefix + ":states", fmt.Sprintf("%s:state:%x", prefix, groupHash[:8])
}

func redisResultInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	case []byte:
		parsed, _ := strconv.ParseInt(string(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func parseRedisLockInt(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}
