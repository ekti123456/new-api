package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	userConcurrencyKeyPrefix       = "concurrency:v2:{slots}:user:"
	totalConcurrencyKey            = "concurrency:v2:{slots}:total"
	activeUserConcurrencyKeyPrefix = "concurrency:v2:{slots}:active:user:"
	activeTotalConcurrencyKey      = "concurrency:v2:{slots}:active:total"
	userConcurrencySlotTTL         = 2 * time.Minute
	userConcurrencyHeartbeat       = 30 * time.Second
)

var acquireUserConcurrencyScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local requestID = ARGV[3]
local totalRequestID = ARGV[4]
local now = tonumber(redis.call('TIME')[1])
local expiresAt = now + ttl
redis.call('ZREMRANGEBYSCORE', key, '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
if redis.call('ZSCORE', key, requestID) ~= false then
  redis.call('ZADD', key, expiresAt, requestID)
  redis.call('ZADD', KEYS[2], expiresAt, totalRequestID)
  redis.call('ZADD', KEYS[3], expiresAt, requestID)
  redis.call('ZADD', KEYS[4], expiresAt, totalRequestID)
  redis.call('EXPIRE', key, ttl)
  redis.call('EXPIRE', KEYS[2], ttl)
  redis.call('EXPIRE', KEYS[3], ttl)
  redis.call('EXPIRE', KEYS[4], ttl)
  return 1
end
if limit > 0 and redis.call('ZCARD', key) >= limit then
  return 0
end
redis.call('ZADD', key, expiresAt, requestID)
redis.call('ZADD', KEYS[2], expiresAt, totalRequestID)
redis.call('ZADD', KEYS[3], expiresAt, requestID)
redis.call('ZADD', KEYS[4], expiresAt, totalRequestID)
redis.call('EXPIRE', key, ttl)
redis.call('EXPIRE', KEYS[2], ttl)
redis.call('EXPIRE', KEYS[3], ttl)
redis.call('EXPIRE', KEYS[4], ttl)
return 1
`)

var refreshUserConcurrencyScript = redis.NewScript(`
local key = KEYS[1]
local ttl = tonumber(ARGV[1])
local requestID = ARGV[2]
local totalRequestID = ARGV[3]
if redis.call('ZSCORE', key, requestID) == false then
  return 0
end
local now = tonumber(redis.call('TIME')[1])
local expiresAt = now + ttl
redis.call('ZADD', key, expiresAt, requestID)
redis.call('ZADD', KEYS[2], expiresAt, totalRequestID)
redis.call('ZADD', KEYS[3], expiresAt, requestID)
redis.call('ZADD', KEYS[4], expiresAt, totalRequestID)
redis.call('EXPIRE', key, ttl)
redis.call('EXPIRE', KEYS[2], ttl)
redis.call('EXPIRE', KEYS[3], ttl)
redis.call('EXPIRE', KEYS[4], ttl)
return 1
`)

var releaseUserConcurrencyScript = redis.NewScript(`
local requestID = ARGV[1]
local totalRequestID = ARGV[2]
local cooldown = tonumber(ARGV[3])
redis.call('ZREM', KEYS[3], requestID)
redis.call('ZREM', KEYS[4], totalRequestID)
if cooldown <= 0 then
  redis.call('ZREM', KEYS[1], requestID)
  redis.call('ZREM', KEYS[2], totalRequestID)
else
  local expiresAt = tonumber(redis.call('TIME')[1]) + cooldown
  redis.call('ZADD', KEYS[1], expiresAt, requestID)
  redis.call('ZADD', KEYS[2], expiresAt, totalRequestID)
end
return 1
`)

var countUserConcurrencyScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', key, '-inf', now)
return redis.call('ZCARD', key)
`)

type localUserConcurrencyState struct {
	mu      sync.Mutex
	active  map[int]map[string]struct{}
	cooling map[int]map[string]time.Time
}

var localUserConcurrency = localUserConcurrencyState{
	active:  make(map[int]map[string]struct{}),
	cooling: make(map[int]map[string]time.Time),
}

// EffectiveUserConcurrencyLimit resolves the raw user override against the
// current system default. A raw value below zero inherits the default; zero is unlimited.
func EffectiveUserConcurrencyLimit(rawLimit int) (limit int, source string) {
	if rawLimit < 0 {
		return setting.DefaultUserConcurrencyLimit, "default"
	}
	if rawLimit == 0 {
		return 0, "unlimited"
	}
	return rawLimit, "user"
}

func userConcurrencyKey(userID int) string {
	return userConcurrencyKeyPrefix + strconv.Itoa(userID)
}

func activeUserConcurrencyKey(userID int) string {
	return activeUserConcurrencyKeyPrefix + strconv.Itoa(userID)
}

func pruneLocalCoolingLocked(userID int, now time.Time) {
	cooling := localUserConcurrency.cooling[userID]
	for requestID, expiresAt := range cooling {
		if !expiresAt.After(now) {
			delete(cooling, requestID)
		}
	}
	if len(cooling) == 0 {
		delete(localUserConcurrency.cooling, userID)
	}
}

func acquireLocalUserConcurrency(userID, limit int, requestID string) bool {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	pruneLocalCoolingLocked(userID, time.Now())
	slots := localUserConcurrency.active[userID]
	if slots == nil {
		slots = make(map[string]struct{})
		localUserConcurrency.active[userID] = slots
	}
	if limit > 0 && len(slots)+len(localUserConcurrency.cooling[userID]) >= limit {
		return false
	}
	slots[requestID] = struct{}{}
	return true
}

func releaseLocalUserConcurrency(userID int, requestID string, cooldown time.Duration) {
	localUserConcurrency.mu.Lock()
	slots := localUserConcurrency.active[userID]
	delete(slots, requestID)
	if len(slots) == 0 {
		delete(localUserConcurrency.active, userID)
	}
	if cooldown > 0 {
		cooling := localUserConcurrency.cooling[userID]
		if cooling == nil {
			cooling = make(map[string]time.Time)
			localUserConcurrency.cooling[userID] = cooling
		}
		cooling[requestID] = time.Now().Add(cooldown)
	}
	localUserConcurrency.mu.Unlock()

	if cooldown > 0 {
		time.AfterFunc(cooldown, func() {
			localUserConcurrency.mu.Lock()
			defer localUserConcurrency.mu.Unlock()
			pruneLocalCoolingLocked(userID, time.Now())
		})
	}
}

func getLocalUserConcurrency(userID int) int {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	return len(localUserConcurrency.active[userID])
}

func getLocalUserOccupiedConcurrency(userID int) int {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	pruneLocalCoolingLocked(userID, time.Now())
	return len(localUserConcurrency.active[userID]) + len(localUserConcurrency.cooling[userID])
}

func getLocalTotalConcurrency() int {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	total := 0
	for _, slots := range localUserConcurrency.active {
		total += len(slots)
	}
	return total
}

func getLocalTotalOccupiedConcurrency() int {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	now := time.Now()
	for userID := range localUserConcurrency.cooling {
		pruneLocalCoolingLocked(userID, now)
	}
	total := 0
	for userID, slots := range localUserConcurrency.active {
		total += len(slots) + len(localUserConcurrency.cooling[userID])
	}
	for userID, cooling := range localUserConcurrency.cooling {
		if _, ok := localUserConcurrency.active[userID]; !ok {
			total += len(cooling)
		}
	}
	return total
}

func totalConcurrencyRequestID(userID int, requestID string) string {
	return strconv.Itoa(userID) + ":" + requestID
}

func acquireUserConcurrency(ctx context.Context, userID, limit int, requestID string) (bool, error) {
	if !common.RedisEnabled {
		return acquireLocalUserConcurrency(userID, limit, requestID), nil
	}
	return acquireUserConcurrencyScript.Run(ctx, common.RDB, []string{userConcurrencyKey(userID), totalConcurrencyKey, activeUserConcurrencyKey(userID), activeTotalConcurrencyKey}, limit, int(userConcurrencySlotTTL.Seconds()), requestID, totalConcurrencyRequestID(userID, requestID)).Bool()
}

func releaseUserConcurrency(userID int, requestID string, cooldown time.Duration) {
	if !common.RedisEnabled {
		releaseLocalUserConcurrency(userID, requestID, cooldown)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := releaseUserConcurrencyScript.Run(ctx, common.RDB, []string{userConcurrencyKey(userID), totalConcurrencyKey, activeUserConcurrencyKey(userID), activeTotalConcurrencyKey}, requestID, totalConcurrencyRequestID(userID, requestID), int(cooldown.Seconds())).Int(); err != nil {
		common.SysLog(fmt.Sprintf("failed to release user concurrency slot for user %d: %v", userID, err))
	}
}

func keepUserConcurrencyAlive(userID int, requestID string, done <-chan struct{}, stopped chan<- struct{}) {
	ticker := time.NewTicker(userConcurrencyHeartbeat)
	defer ticker.Stop()
	defer close(stopped)
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := refreshUserConcurrencyScript.Run(refreshCtx, common.RDB, []string{userConcurrencyKey(userID), totalConcurrencyKey, activeUserConcurrencyKey(userID), activeTotalConcurrencyKey}, int(userConcurrencySlotTTL.Seconds()), requestID, totalConcurrencyRequestID(userID, requestID)).Int()
			cancel()
			if err != nil && !errors.Is(err, redis.Nil) {
				common.SysLog(fmt.Sprintf("failed to refresh user concurrency slot for user %d: %v", userID, err))
			}
		}
	}
}

// GetUserCurrentConcurrency is used by the on-demand admin user card.
func GetUserCurrentConcurrency(ctx context.Context, userID int) (int, error) {
	if !common.RedisEnabled {
		return getLocalUserConcurrency(userID), nil
	}
	return countUserConcurrencyScript.Run(ctx, common.RDB, []string{activeUserConcurrencyKey(userID)}).Int()
}

func GetUserOccupiedConcurrency(ctx context.Context, userID int) (int, error) {
	if !common.RedisEnabled {
		return getLocalUserOccupiedConcurrency(userID), nil
	}
	return countUserConcurrencyScript.Run(ctx, common.RDB, []string{userConcurrencyKey(userID)}).Int()
}

// GetTotalCurrentConcurrency returns the active model request count across all
// users. It reads the shared Redis index in multi-instance deployments and the
// process-local tracker when Redis is disabled.
func GetTotalCurrentConcurrency(ctx context.Context) (int, error) {
	if !common.RedisEnabled {
		return getLocalTotalConcurrency(), nil
	}
	return countUserConcurrencyScript.Run(ctx, common.RDB, []string{activeTotalConcurrencyKey}).Int()
}

func GetTotalOccupiedConcurrency(ctx context.Context) (int, error) {
	if !common.RedisEnabled {
		return getLocalTotalOccupiedConcurrency(), nil
	}
	return countUserConcurrencyScript.Run(ctx, common.RDB, []string{totalConcurrencyKey}).Int()
}

// ModelRequestConcurrencyLimit limits active model requests per authenticated user.
// c.Next does not return until normal HTTP, SSE, or WebSocket handling completes.
func ModelRequestConcurrencyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt("id")
		rawLimit := common.GetContextKeyInt(c, constant.ContextKeyUserConcurrencyLimit)
		limit, _ := EffectiveUserConcurrencyLimit(rawLimit)
		limitEnabled := setting.ModelRequestConcurrencyLimitEnabled
		cooldown := time.Duration(0)
		if !limitEnabled {
			limit = 0
		} else if limit > 0 {
			cooldown = time.Duration(setting.UserConcurrencyCooldownSeconds) * time.Second
		}
		if userID <= 0 {
			c.Next()
			return
		}

		requestID := c.GetString(common.RequestIdKey)
		if requestID == "" {
			requestID = common.NewRequestId()
		}
		acquired, err := acquireUserConcurrency(c.Request.Context(), userID, limit, requestID)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "并发限制服务暂时不可用，请稍后重试")
			return
		}
		if !acquired {
			c.Header("Retry-After", "1")
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("当前并发请求已达到上限（%d），请稍后重试", limit), types.ErrorCode("user_concurrency_limit_exceeded"))
			return
		}

		done := make(chan struct{})
		stopped := make(chan struct{})
		if common.RedisEnabled {
			go keepUserConcurrencyAlive(userID, requestID, done, stopped)
		} else {
			close(stopped)
		}
		defer func() {
			close(done)
			<-stopped
			releaseUserConcurrency(userID, requestID, cooldown)
		}()
		c.Next()
	}
}
