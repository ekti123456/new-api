package requestlimit

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestCounterRollingWindowAndUserIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			now := time.Unix(2000000000, 123000000)
			counter := &Counter{buckets: map[string][]int64{}, now: func() time.Time { return now }}
			oldEnabled, oldRedis := common.RedisEnabled, common.RDB
			t.Cleanup(func() { common.RedisEnabled, common.RDB = oldEnabled, oldRedis })
			common.RedisEnabled = backend == "redis"
			var server *miniredis.Miniredis
			if common.RedisEnabled {
				server = miniredis.RunT(t)
				server.SetTime(now)
				common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
				t.Cleanup(func() { common.RDB.Close() })
			}
			check := func(ns, rule string, id, limit int, charge bool) Usage {
				usage, err := counter.Check(context.Background(), ns, rule, id, limit, charge)
				require.NoError(t, err)
				return usage
			}
			require.True(t, check("one", "rule", 1, 2, true).Allowed)
			now = now.Add(10 * time.Second)
			if server != nil {
				server.SetTime(now)
			}
			require.True(t, check("one", "rule", 1, 2, true).Allowed)
			limited := check("one", "rule", 1, 2, true)
			require.False(t, limited.Allowed)
			require.Equal(t, 50, limited.RetryAfter)
			require.Equal(t, 2, limited.Count)
			require.True(t, check("two", "rule", 1, 2, true).Allowed)
			require.True(t, check("one", "other-rule", 1, 2, true).Allowed)
			require.True(t, check("one", "rule", 2, 2, true).Allowed)
			require.Equal(t, 60, check("one", "rule", 1, 1, false).RetryAfter, "lowering the limit may require more than the oldest call to expire")
			now = now.Add(50 * time.Second)
			if server != nil {
				server.SetTime(now)
			}
			usage := check("one", "rule", 1, 2, false)
			require.True(t, usage.Allowed)
			require.Equal(t, 1, usage.Count)
			now = now.Add(10 * time.Second)
			if server != nil {
				server.SetTime(now)
			}
			usage = check("one", "rule", 1, 2, false)
			require.Zero(t, usage.Count)
			ids, err := counter.ActiveUsers(context.Background(), "one", "rule")
			require.NoError(t, err)
			require.Empty(t, ids)
		})
	}
}
