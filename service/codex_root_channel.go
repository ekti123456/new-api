package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
)

const (
	codexRootChannelCacheNamespace       = "new-api:codex_root_channel:v2"
	codexLegacyRootChannelCacheNamespace = "new-api:codex_root_channel:v1"
	codexRootChannelCacheTTL             = 24 * time.Hour
	codexRootChannelMemoryExpiryLimit    = 100_000
	// Give the exact provisional binding a one-minute safety margin over a
	// freshly published two-minute candidate. Later passive claims still
	// revalidate the binding fingerprint and fail closed if this route expires.
	codexProvisionalRootChannelCacheTTL = 3 * time.Minute
	codexRootChannelRedisTimeout        = 500 * time.Millisecond
	codexRootChannelPollInterval        = 200 * time.Millisecond
)

var ErrCodexRootChannelBindingConflict = errors.New("Codex root channel binding conflict")

// CodexRootChannelBinding pins both the NewAPI channel and its selected key.
// Only a key fingerprint is cached; bearer credentials are never stored here.
type CodexRootChannelBinding struct {
	ChannelID      int    `json:"channel_id"`
	SelectedGroup  string `json:"selected_group"`
	KeyIndex       int    `json:"key_index"`
	KeyFingerprint string `json:"key_fingerprint"`
	UARoutingOnly  bool   `json:"ua_routing_only"`
}

// CodexRootChannelBindingFingerprint identifies the complete immutable route
// represented by a binding. Recent-root candidates and passive aliases carry
// this value so an expired provisional root cannot be rebound to another
// channel/key while an in-flight passive request is claiming its alias.
func CodexRootChannelBindingFingerprint(binding CodexRootChannelBinding) string {
	selectedGroup := strings.TrimSpace(binding.SelectedGroup)
	keyFingerprint := strings.ToLower(strings.TrimSpace(binding.KeyFingerprint))
	if binding.ChannelID <= 0 || selectedGroup == "" || keyFingerprint == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(
		strconv.Itoa(binding.ChannelID) + "\x00" +
			selectedGroup + "\x00" +
			strconv.Itoa(binding.KeyIndex) + "\x00" +
			keyFingerprint + "\x00" +
			strconv.FormatBool(binding.UARoutingOnly),
	))
	return hex.EncodeToString(digest[:])
}

var (
	codexRootChannelCacheOnce       sync.Once
	codexRootChannelCache           *cachex.HybridCache[CodexRootChannelBinding]
	codexLegacyRootChannelCacheOnce sync.Once
	codexLegacyRootChannelCache     *cachex.HybridCache[CodexRootChannelBinding]
	codexRootChannelWriteMu         sync.Mutex
	// Memory mode has no TTL introspection API. Track expirations under the
	// same write lock so an idempotent provisional claim can extend a short
	// binding without shortening an already durable one.
	codexRootChannelMemoryExpiresAt = make(map[string]time.Time)
	codexRootChannelWaiters         = struct {
		sync.Mutex
		items map[string]*codexRootChannelWaiter
	}{items: make(map[string]*codexRootChannelWaiter)}
)

type codexRootChannelWaiter struct {
	updates chan struct{}
	count   int
}

var claimCodexRootChannelBindingScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
  if current == ARGV[1] then
    local current_ttl = redis.call('PTTL', KEYS[1])
    local minimum_ttl = tonumber(ARGV[2])
    if current_ttl >= 0 and current_ttl < minimum_ttl then
      redis.call('PEXPIRE', KEYS[1], minimum_ttl)
    end
  end
  return current
end
if redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2], 'NX') then
  return ARGV[1]
end
return redis.call('GET', KEYS[1])
`)

var storeCodexRootChannelBindingScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current and current ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

func getCodexRootChannelCache() *cachex.HybridCache[CodexRootChannelBinding] {
	codexRootChannelCacheOnce.Do(func() {
		codexRootChannelCache = cachex.NewHybridCache[CodexRootChannelBinding](cachex.HybridCacheConfig[CodexRootChannelBinding]{
			Namespace: cachex.Namespace(codexRootChannelCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[CodexRootChannelBinding]{},
			Memory: func() *hot.HotCache[string, CodexRootChannelBinding] {
				return hot.NewHotCache[string, CodexRootChannelBinding](hot.LRU, 100_000).
					WithTTL(codexRootChannelCacheTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return codexRootChannelCache
}

func getCodexLegacyRootChannelCache() *cachex.HybridCache[CodexRootChannelBinding] {
	codexLegacyRootChannelCacheOnce.Do(func() {
		codexLegacyRootChannelCache = cachex.NewHybridCache[CodexRootChannelBinding](cachex.HybridCacheConfig[CodexRootChannelBinding]{
			Namespace: cachex.Namespace(codexLegacyRootChannelCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[CodexRootChannelBinding]{},
			Memory: func() *hot.HotCache[string, CodexRootChannelBinding] {
				return hot.NewHotCache[string, CodexRootChannelBinding](hot.LRU, 100_000).
					WithTTL(codexRootChannelCacheTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return codexLegacyRootChannelCache
}

func codexRootChannelCacheKey(userID int, rootID string, uaRoutingOnly bool) string {
	rootID = strings.TrimSpace(rootID)
	if userID <= 0 || rootID == "" {
		return ""
	}
	side := "normal"
	if uaRoutingOnly {
		side = "ua"
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + rootID + "\x00" + side))
	return hex.EncodeToString(digest[:])
}

func legacyCodexRootChannelCacheKey(userID int, rootID string) string {
	rootID = strings.TrimSpace(rootID)
	if userID <= 0 || rootID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + rootID))
	return hex.EncodeToString(digest[:])
}

func StoreCodexRootChannelBinding(userID int, rootID string, binding CodexRootChannelBinding) error {
	return storeCodexRootChannelBindingWithTTL(userID, rootID, binding, codexRootChannelCacheTTL)
}

// StoreProvisionalCodexRootChannelBinding makes the root route available
// while its first request is still in flight. A successful response replaces
// it with the durable 24-hour binding; a failed request leaves at most the
// bounded recent-root candidate lifetime.
func StoreProvisionalCodexRootChannelBinding(userID int, rootID string, binding CodexRootChannelBinding) error {
	_, _, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
	return err
}

func storeCodexRootChannelBindingWithTTL(userID int, rootID string, binding CodexRootChannelBinding, ttl time.Duration) error {
	key := codexRootChannelCacheKey(userID, rootID, binding.UARoutingOnly)
	if key == "" || binding.ChannelID <= 0 || strings.TrimSpace(binding.SelectedGroup) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return nil
	}
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(binding)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), codexRootChannelRedisTimeout)
		defer cancel()
		stored, err := storeCodexRootChannelBindingScript.Run(ctx, common.RDB,
			[]string{getCodexRootChannelCache().FullKey(key)}, string(payload), int64(ttl/time.Millisecond)).Int()
		if err != nil {
			return err
		}
		if stored != 1 {
			return ErrCodexRootChannelBindingConflict
		}
		notifyCodexRootChannelBindingUpdate(userID, rootID)
		return nil
	}
	codexRootChannelWriteMu.Lock()
	cache := getCodexRootChannelCache()
	current, found, err := cache.Get(key)
	if err != nil {
		codexRootChannelWriteMu.Unlock()
		return err
	}
	if found && current != binding {
		codexRootChannelWriteMu.Unlock()
		return ErrCodexRootChannelBindingConflict
	}
	if err := cache.SetWithTTL(key, binding, ttl); err != nil {
		codexRootChannelWriteMu.Unlock()
		return err
	}
	trackCodexRootChannelMemoryExpiry(key, time.Now().Add(ttl))
	codexRootChannelWriteMu.Unlock()
	notifyCodexRootChannelBindingUpdate(userID, rootID)
	return nil
}

// ClaimProvisionalCodexRootChannelBinding atomically chooses the first
// channel/key binding observed for a root. Concurrent first turns receive the
// same winner; a caller that selected a different route must abort before
// dispatching upstream so channel setup and quota accounting are not repeated.
func ClaimProvisionalCodexRootChannelBinding(userID int, rootID string, binding CodexRootChannelBinding) (CodexRootChannelBinding, bool, error) {
	key := codexRootChannelCacheKey(userID, rootID, binding.UARoutingOnly)
	if key == "" || binding.ChannelID <= 0 || strings.TrimSpace(binding.SelectedGroup) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return CodexRootChannelBinding{}, false, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(binding)
		if err != nil {
			return CodexRootChannelBinding{}, false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), codexRootChannelRedisTimeout)
		defer cancel()
		winnerResult, err := claimCodexRootChannelBindingScript.Run(ctx, common.RDB,
			[]string{getCodexRootChannelCache().FullKey(key)}, string(payload), int64(codexProvisionalRootChannelCacheTTL/time.Millisecond)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return CodexRootChannelBinding{}, false, err
		}
		winnerPayload := []byte(redisResultString(winnerResult))
		winner := CodexRootChannelBinding{}
		if len(winnerPayload) == 0 || common.Unmarshal(winnerPayload, &winner) != nil {
			return CodexRootChannelBinding{}, false, fmt.Errorf("invalid claimed Codex root channel binding")
		}
		notifyCodexRootChannelBindingUpdate(userID, rootID)
		return winner, winner == binding, nil
	}
	codexRootChannelWriteMu.Lock()
	cache := getCodexRootChannelCache()
	current, found, err := cache.Get(key)
	if err != nil {
		codexRootChannelWriteMu.Unlock()
		return CodexRootChannelBinding{}, false, err
	}
	if found {
		updated := false
		if current == binding {
			minimumExpiry := time.Now().Add(codexProvisionalRootChannelCacheTTL)
			if trackedExpiry, tracked := codexRootChannelMemoryExpiresAt[key]; tracked && trackedExpiry.Before(minimumExpiry) {
				if err := cache.SetWithTTL(key, current, codexProvisionalRootChannelCacheTTL); err != nil {
					codexRootChannelWriteMu.Unlock()
					return CodexRootChannelBinding{}, false, err
				}
				trackCodexRootChannelMemoryExpiry(key, minimumExpiry)
				updated = true
			}
		}
		codexRootChannelWriteMu.Unlock()
		if updated {
			notifyCodexRootChannelBindingUpdate(userID, rootID)
		}
		return current, current == binding, nil
	}
	if err := cache.SetWithTTL(key, binding, codexProvisionalRootChannelCacheTTL); err != nil {
		codexRootChannelWriteMu.Unlock()
		return CodexRootChannelBinding{}, false, err
	}
	trackCodexRootChannelMemoryExpiry(key, time.Now().Add(codexProvisionalRootChannelCacheTTL))
	codexRootChannelWriteMu.Unlock()
	notifyCodexRootChannelBindingUpdate(userID, rootID)
	return binding, true, nil
}

// trackCodexRootChannelMemoryExpiry must be called with
// codexRootChannelWriteMu held.
func trackCodexRootChannelMemoryExpiry(key string, expiresAt time.Time) {
	codexRootChannelMemoryExpiresAt[key] = expiresAt
	if len(codexRootChannelMemoryExpiresAt) <= codexRootChannelMemoryExpiryLimit {
		return
	}
	now := time.Now()
	for trackedKey, trackedExpiry := range codexRootChannelMemoryExpiresAt {
		if !trackedExpiry.After(now) {
			delete(codexRootChannelMemoryExpiresAt, trackedKey)
		}
	}
	// The backing LRU also holds at most this many roots. If its eviction order
	// differs, dropping only the auxiliary timestamp is safe: that root simply
	// falls back to fail-closed behavior instead of having its TTL extended.
	for len(codexRootChannelMemoryExpiresAt) > codexRootChannelMemoryExpiryLimit {
		for trackedKey := range codexRootChannelMemoryExpiresAt {
			delete(codexRootChannelMemoryExpiresAt, trackedKey)
			break
		}
	}
}

func LoadCodexRootChannelBinding(userID int, rootID string) (CodexRootChannelBinding, bool, error) {
	if binding, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, false); err != nil || found {
		return binding, found, err
	}
	return LoadCodexRootChannelBindingForRoutingSide(userID, rootID, true)
}

// LoadCodexRootChannelBindingContext loads an exact root binding across the
// normal and UA-only routing sides while preserving the legacy normal-first
// lookup order.
func LoadCodexRootChannelBindingContext(ctx context.Context, userID int, rootID string) (CodexRootChannelBinding, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if binding, found, err := LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, rootID, false); err != nil || found {
		return binding, found, err
	}
	return LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, rootID, true)
}

// WaitForCodexRootChannelBindingUpdate waits for an exact user/root binding
// publication. In-memory writers wake local waiters immediately; the bounded
// poll interval lets Redis-backed callers observe writes from other instances.
func WaitForCodexRootChannelBindingUpdate(ctx context.Context, userID int, rootID string, maxWait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxWait <= 0 {
		return nil
	}
	waiterKey := legacyCodexRootChannelCacheKey(userID, rootID)
	if waiterKey == "" {
		return nil
	}
	codexRootChannelWaiters.Lock()
	waiter := codexRootChannelWaiters.items[waiterKey]
	if waiter == nil {
		waiter = &codexRootChannelWaiter{updates: make(chan struct{})}
		codexRootChannelWaiters.items[waiterKey] = waiter
	}
	waiter.count++
	codexRootChannelWaiters.Unlock()
	defer func() {
		codexRootChannelWaiters.Lock()
		if current := codexRootChannelWaiters.items[waiterKey]; current == waiter {
			current.count--
			if current.count == 0 {
				delete(codexRootChannelWaiters.items, waiterKey)
			}
		}
		codexRootChannelWaiters.Unlock()
	}()
	waitDuration := maxWait
	if waitDuration > codexRootChannelPollInterval {
		waitDuration = codexRootChannelPollInterval
	}
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-waiter.updates:
		return nil
	case <-timer.C:
		return nil
	}
}

func notifyCodexRootChannelBindingUpdate(userID int, rootID string) {
	waiterKey := legacyCodexRootChannelCacheKey(userID, rootID)
	if waiterKey == "" {
		return
	}
	codexRootChannelWaiters.Lock()
	waiter := codexRootChannelWaiters.items[waiterKey]
	if waiter != nil {
		delete(codexRootChannelWaiters.items, waiterKey)
		close(waiter.updates)
	}
	codexRootChannelWaiters.Unlock()
}

func LoadCodexRootChannelBindingForRoutingSide(userID int, rootID string, uaRoutingOnly bool) (CodexRootChannelBinding, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexRootChannelRedisTimeout)
	defer cancel()
	return LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, rootID, uaRoutingOnly)
}

// LoadCodexRootChannelBindingForRoutingSideContext is the bounded/context-aware
// variant used while resolving passive routes. HybridCache.Get owns an
// independent background timeout, so Redis reads are performed directly here
// to preserve cancellation of the original request and the resolver's shared
// latency budget.
func LoadCodexRootChannelBindingForRoutingSideContext(ctx context.Context, userID int, rootID string, uaRoutingOnly bool) (CodexRootChannelBinding, bool, error) {
	key := codexRootChannelCacheKey(userID, rootID, uaRoutingOnly)
	if key == "" {
		return CodexRootChannelBinding{}, false, nil
	}
	binding, found, err := loadCodexRootChannelBindingCacheValue(ctx, getCodexRootChannelCache(), key)
	if err != nil || found {
		return binding, found, err
	}
	legacyKey := legacyCodexRootChannelCacheKey(userID, rootID)
	legacyBinding, legacyFound, legacyErr := loadCodexRootChannelBindingCacheValue(ctx, getCodexLegacyRootChannelCache(), legacyKey)
	if legacyErr != nil || !legacyFound || legacyBinding.UARoutingOnly != uaRoutingOnly {
		return CodexRootChannelBinding{}, false, legacyErr
	}
	return legacyBinding, true, nil
}

func loadCodexRootChannelBindingCacheValue(ctx context.Context, cache *cachex.HybridCache[CodexRootChannelBinding], key string) (CodexRootChannelBinding, bool, error) {
	if key == "" || cache == nil {
		return CodexRootChannelBinding{}, false, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		payload, err := common.RDB.Get(ctx, cache.FullKey(key)).Bytes()
		if errors.Is(err, redis.Nil) {
			return CodexRootChannelBinding{}, false, nil
		}
		if err != nil {
			return CodexRootChannelBinding{}, false, err
		}
		binding := CodexRootChannelBinding{}
		if err := common.Unmarshal(payload, &binding); err != nil {
			return CodexRootChannelBinding{}, false, err
		}
		return binding, true, nil
	}
	return cache.Get(key)
}
