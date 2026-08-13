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
	userConcurrencyKeyPrefix = "concurrency:{active}:user:"
	totalConcurrencyKey      = "concurrency:{active}:total"
	userConcurrencySlotTTL   = 2 * time.Minute
	userConcurrencyHeartbeat = 30 * time.Second
)

var acquireUserConcurrencyScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local requestID = ARGV[3]
local totalRequestID = ARGV[4]
local now = tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - ttl)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now - ttl)
if redis.call('ZSCORE', key, requestID) ~= false then
  redis.call('ZADD', key, now, requestID)
  redis.call('ZADD', KEYS[2], now, totalRequestID)
  redis.call('EXPIRE', key, ttl)
  redis.call('EXPIRE', KEYS[2], ttl)
  return 1
end
if limit > 0 and redis.call('ZCARD', key) >= limit then
  return 0
end
redis.call('ZADD', key, now, requestID)
redis.call('ZADD', KEYS[2], now, totalRequestID)
redis.call('EXPIRE', key, ttl)
redis.call('EXPIRE', KEYS[2], ttl)
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
redis.call('ZADD', key, now, requestID)
redis.call('ZADD', KEYS[2], now, totalRequestID)
redis.call('EXPIRE', key, ttl)
redis.call('EXPIRE', KEYS[2], ttl)
return 1
`)

var releaseUserConcurrencyScript = redis.NewScript(`
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[2])
return 1
`)

var countUserConcurrencyScript = redis.NewScript(`
local key = KEYS[1]
local ttl = tonumber(ARGV[1])
local now = tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - ttl)
return redis.call('ZCARD', key)
`)

type localUserConcurrencyState struct {
	mu     sync.Mutex
	active map[int]map[string]struct{}
}

var localUserConcurrency = localUserConcurrencyState{active: make(map[int]map[string]struct{})}

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

func acquireLocalUserConcurrency(userID, limit int, requestID string) bool {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	slots := localUserConcurrency.active[userID]
	if slots == nil {
		slots = make(map[string]struct{})
		localUserConcurrency.active[userID] = slots
	}
	if limit > 0 && len(slots) >= limit {
		return false
	}
	slots[requestID] = struct{}{}
	return true
}

func releaseLocalUserConcurrency(userID int, requestID string) {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	slots := localUserConcurrency.active[userID]
	delete(slots, requestID)
	if len(slots) == 0 {
		delete(localUserConcurrency.active, userID)
	}
}

func getLocalUserConcurrency(userID int) int {
	localUserConcurrency.mu.Lock()
	defer localUserConcurrency.mu.Unlock()
	return len(localUserConcurrency.active[userID])
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

func totalConcurrencyRequestID(userID int, requestID string) string {
	return strconv.Itoa(userID) + ":" + requestID
}

func acquireUserConcurrency(ctx context.Context, userID, limit int, requestID string) (bool, error) {
	if !common.RedisEnabled {
		return acquireLocalUserConcurrency(userID, limit, requestID), nil
	}
	return acquireUserConcurrencyScript.Run(ctx, common.RDB, []string{userConcurrencyKey(userID), totalConcurrencyKey}, limit, int(userConcurrencySlotTTL.Seconds()), requestID, totalConcurrencyRequestID(userID, requestID)).Bool()
}

func releaseUserConcurrency(userID int, requestID string) {
	if !common.RedisEnabled {
		releaseLocalUserConcurrency(userID, requestID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := releaseUserConcurrencyScript.Run(ctx, common.RDB, []string{userConcurrencyKey(userID), totalConcurrencyKey}, requestID, totalConcurrencyRequestID(userID, requestID)).Int(); err != nil {
		common.SysLog(fmt.Sprintf("failed to release user concurrency slot for user %d: %v", userID, err))
	}
}

func keepUserConcurrencyAlive(userID int, requestID string, done <-chan struct{}) {
	ticker := time.NewTicker(userConcurrencyHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := refreshUserConcurrencyScript.Run(refreshCtx, common.RDB, []string{userConcurrencyKey(userID), totalConcurrencyKey}, int(userConcurrencySlotTTL.Seconds()), requestID, totalConcurrencyRequestID(userID, requestID)).Int()
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
	return countUserConcurrencyScript.Run(ctx, common.RDB, []string{userConcurrencyKey(userID)}, int(userConcurrencySlotTTL.Seconds())).Int()
}

// GetTotalCurrentConcurrency returns the active model request count across all
// users. It reads the shared Redis index in multi-instance deployments and the
// process-local tracker when Redis is disabled.
func GetTotalCurrentConcurrency(ctx context.Context) (int, error) {
	if !common.RedisEnabled {
		return getLocalTotalConcurrency(), nil
	}
	return countUserConcurrencyScript.Run(ctx, common.RDB, []string{totalConcurrencyKey}, int(userConcurrencySlotTTL.Seconds())).Int()
}

// ModelRequestConcurrencyLimit limits active model requests per authenticated user.
// c.Next does not return until normal HTTP, SSE, or WebSocket handling completes.
func ModelRequestConcurrencyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt("id")
		rawLimit := common.GetContextKeyInt(c, constant.ContextKeyUserConcurrencyLimit)
		limit, _ := EffectiveUserConcurrencyLimit(rawLimit)
		if !setting.ModelRequestConcurrencyLimitEnabled {
			limit = 0
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
		if common.RedisEnabled {
			go keepUserConcurrencyAlive(userID, requestID, done)
		}
		defer func() {
			close(done)
			releaseUserConcurrency(userID, requestID)
		}()
		c.Next()
	}
}
