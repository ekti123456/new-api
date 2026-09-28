package requestlimit

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const windowMS int64 = 60000

type Usage struct {
	Count      int  `json:"count"`
	Limit      int  `json:"limit"`
	RetryAfter int  `json:"retry_after"`
	Allowed    bool `json:"allowed"`
}

type Counter struct {
	mu          sync.Mutex
	buckets     map[string][]int64
	nextCleanup int64
	now         func() time.Time
}

var Default = &Counter{buckets: map[string][]int64{}, now: time.Now}

func rulePrefix(namespace, ruleID string) string {
	return "independent-rpm:{" + namespace + ":" + ruleID + "}:"
}

// Redis TIME gives all instances one clock. Check and charge are one atomic
// operation; rejected requests do not extend the user's occupancy.
var rateScript = redis.NewScript(`
local tm = redis.call('TIME')
local now = tm[1] * 1000 + math.floor(tm[2] / 1000)
local limit = tonumber(ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - 60000)
local count = redis.call('ZCARD', KEYS[1])
local allowed = 0
if count < limit then
  allowed = 1
  if ARGV[2] == 'charge' then
    redis.call('ZADD', KEYS[1], now, ARGV[3])
    redis.call('PEXPIRE', KEYS[1], 120000)
    redis.call('ZADD', KEYS[2], now, ARGV[4])
    redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now - 60000)
    redis.call('PEXPIRE', KEYS[2], 120000)
    count = count + 1
  end
end
local retry = 0
if count >= limit then
  local expires = redis.call('ZRANGE', KEYS[1], count - limit, count - limit, 'WITHSCORES')
  if #expires > 0 then retry = math.max(1, math.ceil((tonumber(expires[2]) + 60000 - now) / 1000)) end
end
return {count, allowed, retry}
`)

func (s *Counter) Check(ctx context.Context, namespace, ruleID string, userID, limit int, charge bool) (Usage, error) {
	result := Usage{Limit: limit}
	prefix := rulePrefix(namespace, ruleID)
	key := prefix + strconv.Itoa(userID)
	if common.RedisEnabled {
		if common.RDB == nil {
			return result, fmt.Errorf("rate limit store unavailable")
		}
		mode := "read"
		if charge {
			mode = "charge"
		}
		values, err := rateScript.Run(ctx, common.RDB, []string{key, prefix + "users"}, limit, mode, uuid.NewString(), userID).Int64Slice()
		if err != nil {
			return result, err
		}
		result.Count, result.Allowed, result.RetryAfter = int(values[0]), values[1] == 1, int(values[2])
		return result, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	if now >= s.nextCleanup {
		for name, times := range s.buckets {
			if len(times) == 0 || times[len(times)-1] <= now-windowMS {
				delete(s.buckets, name)
			}
		}
		s.nextCleanup = now + 10000
	}
	times := s.buckets[key]
	first := sort.Search(len(times), func(i int) bool { return times[i] > now-windowMS })
	times = times[first:]
	result.Allowed = len(times) < limit
	if charge && result.Allowed {
		times = append(times, now)
	}
	if len(times) == 0 {
		delete(s.buckets, key)
	} else {
		s.buckets[key] = times
	}
	result.Count = len(times)
	if result.Count >= limit {
		result.RetryAfter = max(1, int((times[result.Count-limit]+windowMS-now+999)/1000))
	}
	return result, nil
}

// Only the selected rule's active users are enumerated; never SCAN all Redis
// keys or read every user's counters on every request.
func (s *Counter) ActiveUsers(ctx context.Context, namespace, ruleID string) ([]int, error) {
	prefix := rulePrefix(namespace, ruleID)
	var ids []int
	if common.RedisEnabled {
		if common.RDB == nil {
			return nil, fmt.Errorf("rate limit store unavailable")
		}
		values, err := redis.NewScript(`local t=redis.call('TIME'); local n=t[1]*1000+math.floor(t[2]/1000); redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',n-60000); return redis.call('ZRANGE',KEYS[1],0,-1)`).Run(ctx, common.RDB, []string{prefix + "users"}).StringSlice()
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			id, err := strconv.Atoi(value)
			if err == nil && id > 0 {
				ids = append(ids, id)
			}
		}
	} else {
		s.mu.Lock()
		defer s.mu.Unlock()
		now := s.now().UnixMilli()
		for key, times := range s.buckets {
			if len(times) > 0 && times[len(times)-1] > now-windowMS && strings.HasPrefix(key, prefix) {
				id, _ := strconv.Atoi(strings.TrimPrefix(key, prefix))
				if id > 0 {
					ids = append(ids, id)
				}
			}
		}
	}
	sort.Ints(ids)
	return ids, nil
}
