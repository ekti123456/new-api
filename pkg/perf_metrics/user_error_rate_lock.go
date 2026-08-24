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
	maxUserErrorRateUsers     = 100000
	userErrorRateRedisTimeout = 500 * time.Millisecond
)

type UserErrorRateLockStatus struct {
	Locked       bool
	Triggered    bool
	Group        string
	RequestCount int64
	ErrorCount   int64
	ErrorRate    float64
	RetryAfter   int64
	LockSeconds  int
	AccessURL    string
}

type userErrorRateCounter struct {
	Samples       []bool
	Next          int
	Capacity      int
	ErrorCount    int64
	LastUpdatedAt int64
}

type userErrorRateLock struct {
	Group        string
	RequestCount int64
	ErrorCount   int64
	LockedUntil  int64
	LockSeconds  int
	AccessURL    string
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
local max_samples = tonumber(ARGV[2])
local error_value = tonumber(ARGV[3])
redis.call('RPUSH', KEYS[2], error_value)
local error_count = tonumber(redis.call('GET', KEYS[4])) or 0
if error_value == 1 then
  error_count = redis.call('INCR', KEYS[4])
end

local request_count = redis.call('LLEN', KEYS[2])
local overflow = request_count - max_samples
if overflow > 0 then
  local removed_errors = 0
  local removed_samples = redis.call('LRANGE', KEYS[2], 0, overflow - 1)
  for _, removed in ipairs(removed_samples) do
    if tonumber(removed) == 1 then
      removed_errors = removed_errors + 1
    end
  end
  redis.call('LTRIM', KEYS[2], overflow, -1)
  error_count = error_count - removed_errors
  request_count = max_samples
end
if error_count < 0 then
  error_count = 0
end
redis.call('SET', KEYS[4], error_count)
redis.call('SADD', KEYS[3], KEYS[2], KEYS[4])
local state_ttl = tonumber(ARGV[8])
if state_ttl < 1 then
  state_ttl = 1
end
redis.call('EXPIRE', KEYS[2], state_ttl)
redis.call('EXPIRE', KEYS[3], state_ttl)
redis.call('EXPIRE', KEYS[4], state_ttl)

local threshold_millionths = tonumber(ARGV[4])
if request_count >= max_samples and error_count * 100000000 > request_count * threshold_millionths then
  local lock_seconds = tonumber(ARGV[5])
  redis.call('HSET', KEYS[1],
    'group', ARGV[6],
    'request_count', request_count,
    'error_count', error_count,
    'locked_until', now + lock_seconds,
    'lock_seconds', lock_seconds,
    'access_url', ARGV[7])
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

var clearUserErrorRateSamplesScript = redis.NewScript(`
local state_keys = redis.call('SMEMBERS', KEYS[1])
for _, state_key in ipairs(state_keys) do
  redis.call('DEL', state_key)
end
redis.call('DEL', KEYS[1])
return #state_keys
`)

func ObserveUserErrorRate(info *relaycommon.RelayInfo, user *UserMetricIdentity) UserErrorRateLockStatus {
	if !perf_metrics_setting.IsUserErrorRateLockEnabled() || info == nil || info.ExcludeFromPerformanceMetrics || user == nil || user.UserID <= 0 {
		return UserErrorRateLockStatus{}
	}
	eligibilityDeadline, eligible := getUserErrorRateEligibilityDeadline(user.UserID)
	if !eligible {
		return UserErrorRateLockStatus{}
	}
	group := info.UsingGroup
	if !perf_metrics_setting.IsUserAnomalyGroupMonitored(group) {
		return UserErrorRateLockStatus{}
	}

	isError := !IsRelaySampleSuccess(info)
	if common.RedisEnabled && common.RDB != nil {
		status, err := observeUserErrorRateRedis(user.UserID, group, user.AccessURL, isError, eligibilityDeadline)
		if err != nil {
			common.SysLog(fmt.Sprintf("user error-rate lock observation failed for user %d: %v", user.UserID, err))
			return UserErrorRateLockStatus{}
		}
		return status
	}
	return observeUserErrorRateMemory(user.UserID, group, user.AccessURL, isError)
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

func observeUserErrorRateMemory(userID int, group string, accessURL string, isError bool) UserErrorRateLockStatus {
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
			purgeExpiredUserErrorRateLocksLocked(now)
			if len(userErrorRateMemory.states)+len(userErrorRateMemory.locks) >= maxUserErrorRateUsers {
				if !evictOldestUserErrorRateStateLocked() {
					return UserErrorRateLockStatus{}
				}
			}
		}
		states = make(map[string]*userErrorRateCounter)
		userErrorRateMemory.states[userID] = states
	}

	maxSamples := perf_metrics_setting.GetUserErrorRateLockMinRequests()
	counter := states[group]
	if counter == nil {
		counter = &userErrorRateCounter{Capacity: maxSamples}
		states[group] = counter
	}
	counter.addSample(isError, maxSamples, now)
	requestCount := int64(len(counter.Samples))

	threshold := perf_metrics_setting.GetUserErrorRateLockThreshold()
	if requestCount < int64(maxSamples) || float64(counter.ErrorCount)*100 <= float64(requestCount)*threshold {
		return UserErrorRateLockStatus{}
	}

	lockSeconds := perf_metrics_setting.GetUserErrorRateLockSeconds()
	lock := userErrorRateLock{
		Group:        group,
		RequestCount: requestCount,
		ErrorCount:   counter.ErrorCount,
		LockedUntil:  now + int64(lockSeconds),
		LockSeconds:  lockSeconds,
		AccessURL:    accessURL,
	}
	delete(userErrorRateMemory.states, userID)
	userErrorRateMemory.locks[userID] = lock
	status := buildUserErrorRateLockStatus(lock, now)
	status.Triggered = true
	return status
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

func (counter *userErrorRateCounter) addSample(isError bool, capacity int, now int64) {
	if capacity < 1 {
		return
	}
	if counter.Capacity != capacity {
		counter.resize(capacity)
	}
	counter.LastUpdatedAt = now
	if len(counter.Samples) < capacity {
		counter.Samples = append(counter.Samples, isError)
		if isError {
			counter.ErrorCount++
		}
		return
	}
	if counter.Samples[counter.Next] {
		counter.ErrorCount--
	}
	counter.Samples[counter.Next] = isError
	if isError {
		counter.ErrorCount++
	}
	counter.Next = (counter.Next + 1) % capacity
}

func (counter *userErrorRateCounter) resize(capacity int) {
	ordered := counter.orderedSamples()
	if len(ordered) > capacity {
		ordered = ordered[len(ordered)-capacity:]
	}
	counter.Samples = append([]bool(nil), ordered...)
	counter.Capacity = capacity
	counter.Next = 0
	counter.ErrorCount = 0
	for _, isError := range counter.Samples {
		if isError {
			counter.ErrorCount++
		}
	}
}

func (counter *userErrorRateCounter) orderedSamples() []bool {
	if len(counter.Samples) == 0 {
		return nil
	}
	if len(counter.Samples) < counter.Capacity || counter.Next == 0 {
		return append([]bool(nil), counter.Samples...)
	}
	ordered := make([]bool, 0, len(counter.Samples))
	ordered = append(ordered, counter.Samples[counter.Next:]...)
	ordered = append(ordered, counter.Samples[:counter.Next]...)
	return ordered
}

func purgeExpiredUserErrorRateLocksLocked(now int64) {
	for userID, lock := range userErrorRateMemory.locks {
		if lock.LockedUntil <= now {
			delete(userErrorRateMemory.locks, userID)
		}
	}
}

func evictOldestUserErrorRateStateLocked() bool {
	oldestUserID := 0
	oldestUpdatedAt := int64(0)
	for userID, states := range userErrorRateMemory.states {
		latestUpdatedAt := int64(0)
		for _, state := range states {
			if state.LastUpdatedAt > latestUpdatedAt {
				latestUpdatedAt = state.LastUpdatedAt
			}
		}
		if oldestUserID == 0 || latestUpdatedAt < oldestUpdatedAt {
			oldestUserID = userID
			oldestUpdatedAt = latestUpdatedAt
		}
	}
	if oldestUserID != 0 {
		delete(userErrorRateMemory.states, oldestUserID)
		return true
	}
	return false
}

func clearUserErrorRateSamples(userID int) {
	if userID <= 0 {
		return
	}
	userErrorRateMemory.Lock()
	delete(userErrorRateMemory.states, userID)
	userErrorRateMemory.Unlock()

	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	_, statesKey, _, _ := userErrorRateRedisKeys(userID, "")
	ctx, cancel := context.WithTimeout(context.Background(), userErrorRateRedisTimeout)
	defer cancel()
	if err := clearUserErrorRateSamplesScript.Run(ctx, common.RDB, []string{statesKey}).Err(); err != nil && err != redis.Nil {
		common.SysLog(fmt.Sprintf("user error-rate sample cleanup failed for user %d: %v", userID, err))
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
		AccessURL:    lock.AccessURL,
	}
}

func observeUserErrorRateRedis(userID int, group string, accessURL string, isError bool, eligibilityDeadline int64) (UserErrorRateLockStatus, error) {
	lockKey, statesKey, stateKey, errorCountKey := userErrorRateRedisKeys(userID, group)
	now := userErrorRateNow().Unix()
	errorValue := 0
	if isError {
		errorValue = 1
	}
	thresholdMillionths := int64(math.Round(perf_metrics_setting.GetUserErrorRateLockThreshold() * 1000000))
	lockSeconds := perf_metrics_setting.GetUserErrorRateLockSeconds()
	maxSamples := perf_metrics_setting.GetUserErrorRateLockMinRequests()
	stateTTL := eligibilityDeadline - now + int64(perf_metrics_setting.UserAnomalyRetentionHours*3600)
	if stateTTL < 1 {
		stateTTL = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), userErrorRateRedisTimeout)
	defer cancel()
	result, err := observeUserErrorRateScript.Run(ctx, common.RDB, []string{lockKey, stateKey, statesKey, errorCountKey},
		now,
		maxSamples,
		errorValue,
		thresholdMillionths,
		lockSeconds,
		group,
		accessURL,
		stateTTL,
	).Slice()
	if err != nil {
		return UserErrorRateLockStatus{}, err
	}
	if len(result) < 3 || redisResultInt64(result[0]) != 1 {
		return UserErrorRateLockStatus{}, nil
	}
	requestCount := redisResultInt64(result[1])
	errorCount := redisResultInt64(result[2])
	status := buildUserErrorRateLockStatus(userErrorRateLock{
		Group:        group,
		RequestCount: requestCount,
		ErrorCount:   errorCount,
		LockedUntil:  now + int64(lockSeconds),
		LockSeconds:  lockSeconds,
		AccessURL:    accessURL,
	}, now)
	status.Triggered = true
	return status, nil
}

func getUserErrorRateLockRedis(userID int) (UserErrorRateLockStatus, error) {
	lockKey, _, _, _ := userErrorRateRedisKeys(userID, "")
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
		AccessURL:    values["access_url"],
	}
	return buildUserErrorRateLockStatus(lock, now), nil
}

func userErrorRateRedisKeys(userID int, group string) (string, string, string, string) {
	tag := fmt.Sprintf("{%d}", userID)
	groupHash := sha256.Sum256([]byte(group))
	prefix := "perf:user-error-lock:v2:" + tag
	statePrefix := fmt.Sprintf("%s:state:%x", prefix, groupHash[:8])
	return prefix + ":lock", prefix + ":states", statePrefix + ":samples", statePrefix + ":errors"
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
