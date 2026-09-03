package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/samber/hot"
)

const (
	codexTurnRootCacheNamespace    = "new-api:codex_lineage_root:v1"
	codexTurnRootMemoryExpiryLimit = 300_000
)

var (
	ErrCodexTurnRootBindingConflict  = errors.New("Codex turn root binding conflict")
	ErrCodexTurnRootBindingInvalid   = errors.New("invalid Codex turn root binding")
	ErrCodexTurnRootRouteUnavailable = errors.New("Codex turn root route is unavailable")
)

var claimCodexLineageRootBindingScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
  if current == ARGV[1] then
    local current_ttl = redis.call('PTTL', KEYS[1])
    local minimum_ttl = tonumber(ARGV[2])
    if current_ttl >= 0 and current_ttl < minimum_ttl then
      redis.call('PEXPIRE', KEYS[1], minimum_ttl)
    end
    -- Once another request observes the same provisional mapping, the
    -- creator no longer owns it exclusively and therefore must not roll it
    -- back underneath the joining request.
    redis.call('DEL', KEYS[2])
  end
  return {current, 0}
end
if redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2], 'NX') then
  redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[2])
  return {ARGV[1], 1}
end
return {redis.call('GET', KEYS[1]), 0}
`)

var releaseCodexLineageRootBindingScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[2])
if not owner or owner ~= ARGV[2] then
  return 0
end
local current = redis.call('GET', KEYS[1])
if not current or current ~= ARGV[1] then
  return 0
end
local current_ttl = redis.call('PTTL', KEYS[1])
if current_ttl < 0 or current_ttl > tonumber(ARGV[3]) then
  return 0
end
redis.call('DEL', KEYS[2])
return redis.call('DEL', KEYS[1])
`)

// CodexTurnRootBinding records only a verified route reference. The current
// root binding is reloaded and fingerprint-checked on every use, so a stale or
// forged turn identifier can never select a different channel or key.
type CodexTurnRootBinding struct {
	RootID             string `json:"root_id"`
	SelectedGroup      string `json:"selected_group"`
	BindingFingerprint string `json:"binding_fingerprint"`
	UARoutingOnly      bool   `json:"ua_routing_only"`
	RootOwner          bool   `json:"root_owner"`
	Related            bool   `json:"related"`
	PassiveFeature     string `json:"passive_feature,omitempty"`
	ThreadSource       string `json:"thread_source,omitempty"`
	RequestKind        string `json:"request_kind,omitempty"`
	SubagentKind       string `json:"subagent_kind,omitempty"`
}

type CodexTurnRouteIdentity struct {
	RootOwner      bool
	Related        bool
	PassiveFeature string
	ThreadSource   string
	RequestKind    string
	SubagentKind   string
}

func (binding CodexTurnRootBinding) RouteIdentity() CodexTurnRouteIdentity {
	return CodexTurnRouteIdentity{
		RootOwner: binding.RootOwner, Related: binding.Related,
		PassiveFeature: binding.PassiveFeature, ThreadSource: binding.ThreadSource,
		RequestKind: binding.RequestKind, SubagentKind: binding.SubagentKind,
	}
}

var (
	codexTurnRootCacheOnce       sync.Once
	codexTurnRootCache           *cachex.HybridCache[CodexTurnRootBinding]
	codexTurnRootWriteMu         sync.Mutex
	codexTurnRootMemoryExpiresAt = make(map[string]time.Time)
	codexTurnRootRollbackOwners  = make(map[string]codexLineageRollbackOwner)
	codexTurnRootWaiters         = struct {
		sync.Mutex
		items map[string]*codexRootChannelWaiter
	}{items: make(map[string]*codexRootChannelWaiter)}
)

type codexLineageRollbackOwner struct {
	token     string
	expiresAt time.Time
}

func getCodexTurnRootCache() *cachex.HybridCache[CodexTurnRootBinding] {
	codexTurnRootCacheOnce.Do(func() {
		codexTurnRootCache = cachex.NewHybridCache[CodexTurnRootBinding](cachex.HybridCacheConfig[CodexTurnRootBinding]{
			Namespace: cachex.Namespace(codexTurnRootCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[CodexTurnRootBinding]{},
			Memory: func() *hot.HotCache[string, CodexTurnRootBinding] {
				return hot.NewHotCache[string, CodexTurnRootBinding](hot.LRU, codexTurnRootMemoryExpiryLimit).
					WithTTL(codexRootChannelCacheTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return codexTurnRootCache
}

func normalizeCodexTurnID(turnID string) string {
	parsed, err := uuid.Parse(strings.TrimSpace(turnID))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.String())
}

func codexTurnRootCacheKey(userID int, turnID string) string {
	return codexLineageRootCacheKey(userID, "turn", turnID)
}

func codexThreadRootCacheKey(userID int, threadID string, uaRoutingOnly bool) string {
	kind := "thread_normal"
	if uaRoutingOnly {
		kind = "thread_ua"
	}
	return codexLineageRootCacheKey(userID, kind, threadID)
}

func codexThreadRootWaiterKey(userID int, threadID string) string {
	return codexLineageRootCacheKey(userID, "thread_wait", threadID)
}

func codexLineageRootRollbackOwnerKey(key string) string {
	if key == "" {
		return ""
	}
	return key + ":rollback_owner"
}

func codexLineageRootCacheKey(userID int, kind, identifier string) string {
	identifier = normalizeCodexTurnID(identifier)
	if userID <= 0 || identifier == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + kind + "\x00" + identifier))
	return hex.EncodeToString(digest[:])
}

func normalizeCodexTurnRouteIdentity(identity CodexTurnRouteIdentity) (CodexTurnRouteIdentity, bool) {
	identity.PassiveFeature = strings.ToLower(strings.TrimSpace(identity.PassiveFeature))
	identity.ThreadSource = strings.ToLower(strings.TrimSpace(identity.ThreadSource))
	identity.RequestKind = strings.ToLower(strings.TrimSpace(identity.RequestKind))
	identity.SubagentKind = strings.ToLower(strings.TrimSpace(identity.SubagentKind))
	if invalidCodexTurnRouteLabel(identity.ThreadSource, 128) ||
		invalidCodexTurnRouteLabel(identity.RequestKind, 128) ||
		invalidCodexTurnRouteLabel(identity.SubagentKind, 64) {
		return CodexTurnRouteIdentity{}, false
	}
	if identity.RootOwner {
		if identity.PassiveFeature != "" ||
			(identity.Related && identity.ThreadSource != "user") ||
			(identity.ThreadSource == "user" && identity.SubagentKind != "") {
			return CodexTurnRouteIdentity{}, false
		}
		return identity, true
	}
	if !identity.Related || identity.ThreadSource == "" || identity.ThreadSource == "user" {
		return CodexTurnRouteIdentity{}, false
	}
	switch identity.PassiveFeature {
	case "related_internal":
	case "system_passive":
		if identity.ThreadSource != "system" {
			return CodexTurnRouteIdentity{}, false
		}
	default:
		return CodexTurnRouteIdentity{}, false
	}
	return identity, true
}

func invalidCodexTurnRouteLabel(value string, limit int) bool {
	return len(value) > limit || strings.ContainsAny(value, "\r\n\x00")
}

func codexTurnRootBindingForRoute(rootID string, binding CodexRootChannelBinding, identity CodexTurnRouteIdentity) (CodexTurnRootBinding, bool) {
	parsedRoot, err := uuid.Parse(strings.TrimSpace(rootID))
	if err != nil {
		return CodexTurnRootBinding{}, false
	}
	selectedGroup := strings.TrimSpace(binding.SelectedGroup)
	bindingFingerprint := CodexRootChannelBindingFingerprint(binding)
	identity, identityValid := normalizeCodexTurnRouteIdentity(identity)
	if selectedGroup == "" || bindingFingerprint == "" || !identityValid {
		return CodexTurnRootBinding{}, false
	}
	return CodexTurnRootBinding{
		RootID:             strings.ToLower(parsedRoot.String()),
		SelectedGroup:      selectedGroup,
		BindingFingerprint: bindingFingerprint,
		UARoutingOnly:      binding.UARoutingOnly,
		RootOwner:          identity.RootOwner,
		Related:            identity.Related,
		PassiveFeature:     identity.PassiveFeature,
		ThreadSource:       identity.ThreadSource,
		RequestKind:        identity.RequestKind,
		SubagentKind:       identity.SubagentKind,
	}, true
}

func normalizeCodexTurnRootBinding(binding CodexTurnRootBinding) (CodexTurnRootBinding, bool) {
	parsedRoot, err := uuid.Parse(strings.TrimSpace(binding.RootID))
	if err != nil {
		return CodexTurnRootBinding{}, false
	}
	binding.RootID = strings.ToLower(parsedRoot.String())
	binding.SelectedGroup = strings.TrimSpace(binding.SelectedGroup)
	binding.BindingFingerprint = strings.ToLower(strings.TrimSpace(binding.BindingFingerprint))
	identity, identityValid := normalizeCodexTurnRouteIdentity(binding.RouteIdentity())
	if binding.SelectedGroup == "" || len(binding.BindingFingerprint) != sha256.Size*2 || !identityValid {
		return CodexTurnRootBinding{}, false
	}
	binding.RootOwner = identity.RootOwner
	binding.Related = identity.Related
	binding.PassiveFeature = identity.PassiveFeature
	binding.ThreadSource = identity.ThreadSource
	binding.RequestKind = identity.RequestKind
	binding.SubagentKind = identity.SubagentKind
	if _, err := hex.DecodeString(binding.BindingFingerprint); err != nil {
		return CodexTurnRootBinding{}, false
	}
	return binding, true
}

// ClaimProvisionalCodexTurnRootBinding publishes a turn lineage before the
// upstream request starts. The first verified root wins; retries may extend the
// three-minute provisional lifetime but can never shorten a durable mapping.
// A non-empty rollback token is returned only while the creator is the sole
// claimant of this value.
func ClaimProvisionalCodexTurnRootBinding(userID int, turnID, rootID string, rootBinding CodexRootChannelBinding, identity CodexTurnRouteIdentity) (CodexTurnRootBinding, bool, bool, string, error) {
	key := codexTurnRootCacheKey(userID, turnID)
	binding, valid := codexTurnRootBindingForRoute(rootID, rootBinding, identity)
	winner, won, created, rollbackToken, err := claimProvisionalCodexLineageRootBinding(key, binding, valid)
	if err == nil && key != "" && valid {
		notifyCodexLineageRootBindingUpdate(key)
	}
	return winner, won, created, rollbackToken, err
}

// ClaimProvisionalCodexThreadRootBinding records the verified root of a native
// Codex thread. It lets forked_from_thread_id resolve a source child thread
// without incorrectly treating that Thread ID as a root Session ID.
func ClaimProvisionalCodexThreadRootBinding(userID int, threadID, rootID string, rootBinding CodexRootChannelBinding) (CodexTurnRootBinding, bool, bool, string, error) {
	key := codexThreadRootCacheKey(userID, threadID, rootBinding.UARoutingOnly)
	binding, valid := codexTurnRootBindingForRoute(rootID, rootBinding, CodexTurnRouteIdentity{RootOwner: true})
	winner, won, created, rollbackToken, err := claimProvisionalCodexLineageRootBinding(key, binding, valid)
	if err == nil && key != "" && valid {
		notifyCodexLineageRootBindingUpdate(codexThreadRootWaiterKey(userID, threadID))
	}
	return winner, won, created, rollbackToken, err
}

// ReleaseProvisionalCodexTurnRootBinding removes a Turn mapping only when it
// still contains the exact value created by this request and has not been
// promoted to the durable lifetime. Its one-time token must also remain
// exclusive: an identical joining claim invalidates that token. It is used to
// roll back a multi-stage lineage claim that fails before anything is sent
// upstream.
func ReleaseProvisionalCodexTurnRootBinding(userID int, turnID string, expected CodexTurnRootBinding, rollbackToken string) (bool, error) {
	key := codexTurnRootCacheKey(userID, turnID)
	released, err := releaseProvisionalCodexLineageRootBinding(key, expected, rollbackToken)
	if err == nil && released {
		notifyCodexLineageRootBindingUpdate(key)
	}
	return released, err
}

// ReleaseProvisionalCodexThreadRootBinding is the Thread counterpart of
// ReleaseProvisionalCodexTurnRootBinding. The routing side is part of the
// expected value, so a normal-side rollback cannot delete a UA-only mapping.
func ReleaseProvisionalCodexThreadRootBinding(userID int, threadID string, expected CodexTurnRootBinding, rollbackToken string) (bool, error) {
	key := codexThreadRootCacheKey(userID, threadID, expected.UARoutingOnly)
	released, err := releaseProvisionalCodexLineageRootBinding(key, expected, rollbackToken)
	if err == nil && released {
		notifyCodexLineageRootBindingUpdate(codexThreadRootWaiterKey(userID, threadID))
	}
	return released, err
}

func claimProvisionalCodexLineageRootBinding(key string, binding CodexTurnRootBinding, valid bool) (CodexTurnRootBinding, bool, bool, string, error) {
	if key == "" || !valid {
		return CodexTurnRootBinding{}, false, false, "", nil
	}
	rollbackToken := uuid.NewString()
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(binding)
		if err != nil {
			return CodexTurnRootBinding{}, false, false, "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), codexRootChannelRedisTimeout)
		defer cancel()
		winnerResult, err := claimCodexLineageRootBindingScript.Run(ctx, common.RDB,
			[]string{
				getCodexTurnRootCache().FullKey(key),
				getCodexTurnRootCache().FullKey(codexLineageRootRollbackOwnerKey(key)),
			}, string(payload), int64(codexProvisionalRootChannelCacheTTL/time.Millisecond), rollbackToken).Slice()
		if err != nil && !errors.Is(err, redis.Nil) {
			return CodexTurnRootBinding{}, false, false, "", err
		}
		winner := CodexTurnRootBinding{}
		if len(winnerResult) != 2 {
			return CodexTurnRootBinding{}, false, false, "", ErrCodexTurnRootBindingInvalid
		}
		if raw := []byte(redisResultString(winnerResult[0])); len(raw) == 0 || common.Unmarshal(raw, &winner) != nil {
			return CodexTurnRootBinding{}, false, false, "", ErrCodexTurnRootBindingInvalid
		}
		winner, valid = normalizeCodexTurnRootBinding(winner)
		if !valid {
			return CodexTurnRootBinding{}, false, false, "", ErrCodexTurnRootBindingInvalid
		}
		created, ok := winnerResult[1].(int64)
		if !ok || (created != 0 && created != 1) {
			return CodexTurnRootBinding{}, false, false, "", ErrCodexTurnRootBindingInvalid
		}
		if created != 1 {
			rollbackToken = ""
		}
		return winner, winner == binding, created == 1, rollbackToken, nil
	}

	codexTurnRootWriteMu.Lock()
	cache := getCodexTurnRootCache()
	current, found, err := cache.Get(key)
	if err != nil {
		codexTurnRootWriteMu.Unlock()
		return CodexTurnRootBinding{}, false, false, "", err
	}
	if found {
		current, valid = normalizeCodexTurnRootBinding(current)
		if !valid {
			codexTurnRootWriteMu.Unlock()
			return CodexTurnRootBinding{}, false, false, "", ErrCodexTurnRootBindingInvalid
		}
		if current == binding {
			delete(codexTurnRootRollbackOwners, key)
			minimumExpiry := time.Now().Add(codexProvisionalRootChannelCacheTTL)
			if trackedExpiry, tracked := codexTurnRootMemoryExpiresAt[key]; tracked && trackedExpiry.Before(minimumExpiry) {
				if err := cache.SetWithTTL(key, current, codexProvisionalRootChannelCacheTTL); err != nil {
					codexTurnRootWriteMu.Unlock()
					return CodexTurnRootBinding{}, false, false, "", err
				}
				trackCodexTurnRootMemoryExpiry(key, minimumExpiry)
			}
		}
		codexTurnRootWriteMu.Unlock()
		return current, current == binding, false, "", nil
	}
	if err := cache.SetWithTTL(key, binding, codexProvisionalRootChannelCacheTTL); err != nil {
		codexTurnRootWriteMu.Unlock()
		return CodexTurnRootBinding{}, false, false, "", err
	}
	expiresAt := time.Now().Add(codexProvisionalRootChannelCacheTTL)
	trackCodexTurnRootMemoryExpiry(key, expiresAt)
	codexTurnRootRollbackOwners[key] = codexLineageRollbackOwner{token: rollbackToken, expiresAt: expiresAt}
	codexTurnRootWriteMu.Unlock()
	return binding, true, true, rollbackToken, nil
}

func releaseProvisionalCodexLineageRootBinding(key string, expected CodexTurnRootBinding, rollbackToken string) (bool, error) {
	expected, valid := normalizeCodexTurnRootBinding(expected)
	rollbackToken = strings.TrimSpace(rollbackToken)
	if key == "" || !valid || rollbackToken == "" {
		return false, nil
	}
	cache := getCodexTurnRootCache()
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(expected)
		if err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), codexRootChannelRedisTimeout)
		defer cancel()
		released, err := releaseCodexLineageRootBindingScript.Run(ctx, common.RDB,
			[]string{
				cache.FullKey(key),
				cache.FullKey(codexLineageRootRollbackOwnerKey(key)),
			}, string(payload), rollbackToken, int64(codexProvisionalRootChannelCacheTTL/time.Millisecond)).Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			return false, err
		}
		return released == 1, nil
	}

	codexTurnRootWriteMu.Lock()
	defer codexTurnRootWriteMu.Unlock()
	current, found, err := cache.Get(key)
	if err != nil || !found {
		return false, err
	}
	current, valid = normalizeCodexTurnRootBinding(current)
	if !valid {
		return false, ErrCodexTurnRootBindingInvalid
	}
	if current != expected {
		return false, nil
	}
	owner, owned := codexTurnRootRollbackOwners[key]
	if !owned || owner.token != rollbackToken || !owner.expiresAt.After(time.Now()) {
		return false, nil
	}
	expiresAt, tracked := codexTurnRootMemoryExpiresAt[key]
	if !tracked || !expiresAt.After(time.Now()) || expiresAt.After(time.Now().Add(codexProvisionalRootChannelCacheTTL)) {
		return false, nil
	}
	results, err := cache.DeleteMany([]string{key})
	if err != nil {
		return false, err
	}
	released := results[cache.FullKey(key)]
	if released {
		delete(codexTurnRootMemoryExpiresAt, key)
		delete(codexTurnRootRollbackOwners, key)
	}
	return released, nil
}

// StoreCodexTurnRootBinding promotes an already verified turn to the same
// 24-hour lifetime as its root route. A conflicting mapping is never replaced.
func StoreCodexTurnRootBinding(userID int, turnID, rootID string, rootBinding CodexRootChannelBinding, identity CodexTurnRouteIdentity) error {
	key := codexTurnRootCacheKey(userID, turnID)
	binding, valid := codexTurnRootBindingForRoute(rootID, rootBinding, identity)
	err := storeCodexLineageRootBinding(key, binding, valid)
	if err == nil && key != "" && valid {
		notifyCodexLineageRootBindingUpdate(key)
	}
	return err
}

// StoreCodexThreadRootBinding promotes a verified thread route to the same
// durable lifetime as the owning root session.
func StoreCodexThreadRootBinding(userID int, threadID, rootID string, rootBinding CodexRootChannelBinding) error {
	key := codexThreadRootCacheKey(userID, threadID, rootBinding.UARoutingOnly)
	binding, valid := codexTurnRootBindingForRoute(rootID, rootBinding, CodexTurnRouteIdentity{RootOwner: true})
	err := storeCodexLineageRootBinding(key, binding, valid)
	if err == nil && key != "" && valid {
		notifyCodexLineageRootBindingUpdate(codexThreadRootWaiterKey(userID, threadID))
	}
	return err
}

func storeCodexLineageRootBinding(key string, binding CodexTurnRootBinding, valid bool) error {
	if key == "" || !valid {
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
			[]string{getCodexTurnRootCache().FullKey(key)}, string(payload), int64(codexRootChannelCacheTTL/time.Millisecond)).Int()
		if err != nil {
			return err
		}
		if stored != 1 {
			return ErrCodexTurnRootBindingConflict
		}
		return nil
	}

	codexTurnRootWriteMu.Lock()
	cache := getCodexTurnRootCache()
	current, found, err := cache.Get(key)
	if err != nil {
		codexTurnRootWriteMu.Unlock()
		return err
	}
	if found {
		current, valid = normalizeCodexTurnRootBinding(current)
		if !valid {
			codexTurnRootWriteMu.Unlock()
			return ErrCodexTurnRootBindingInvalid
		}
		if current != binding {
			codexTurnRootWriteMu.Unlock()
			return ErrCodexTurnRootBindingConflict
		}
	}
	if err := cache.SetWithTTL(key, binding, codexRootChannelCacheTTL); err != nil {
		codexTurnRootWriteMu.Unlock()
		return err
	}
	trackCodexTurnRootMemoryExpiry(key, time.Now().Add(codexRootChannelCacheTTL))
	delete(codexTurnRootRollbackOwners, key)
	codexTurnRootWriteMu.Unlock()
	return nil
}

func trackCodexTurnRootMemoryExpiry(key string, expiresAt time.Time) {
	codexTurnRootMemoryExpiresAt[key] = expiresAt
	if len(codexTurnRootMemoryExpiresAt) <= codexTurnRootMemoryExpiryLimit {
		return
	}
	now := time.Now()
	for trackedKey, trackedExpiry := range codexTurnRootMemoryExpiresAt {
		if !trackedExpiry.After(now) {
			delete(codexTurnRootMemoryExpiresAt, trackedKey)
			delete(codexTurnRootRollbackOwners, trackedKey)
		}
	}
	for len(codexTurnRootMemoryExpiresAt) > codexTurnRootMemoryExpiryLimit {
		for trackedKey := range codexTurnRootMemoryExpiresAt {
			delete(codexTurnRootMemoryExpiresAt, trackedKey)
			delete(codexTurnRootRollbackOwners, trackedKey)
			break
		}
	}
}

func LoadCodexTurnRootBindingContext(ctx context.Context, userID int, turnID string) (CodexTurnRootBinding, bool, error) {
	key := codexTurnRootCacheKey(userID, turnID)
	return loadCodexLineageRootBindingContext(ctx, key)
}

func LoadCodexThreadRootBindingContext(ctx context.Context, userID int, threadID string) (CodexTurnRootBinding, bool, error) {
	normal, normalFound, err := loadCodexLineageRootBindingContext(ctx, codexThreadRootCacheKey(userID, threadID, false))
	if err != nil {
		return CodexTurnRootBinding{}, false, err
	}
	uaOnly, uaFound, err := loadCodexLineageRootBindingContext(ctx, codexThreadRootCacheKey(userID, threadID, true))
	if err != nil {
		return CodexTurnRootBinding{}, false, err
	}
	if normalFound && uaFound {
		return CodexTurnRootBinding{}, false, ErrCodexTurnRootBindingConflict
	}
	if normalFound {
		return normal, true, nil
	}
	return uaOnly, uaFound, nil
}

func loadCodexLineageRootBindingContext(ctx context.Context, key string) (CodexTurnRootBinding, bool, error) {
	if key == "" {
		return CodexTurnRootBinding{}, false, nil
	}
	cache := getCodexTurnRootCache()
	var binding CodexTurnRootBinding
	var found bool
	var err error
	if common.RedisEnabled && common.RDB != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		payload, redisErr := common.RDB.Get(ctx, cache.FullKey(key)).Bytes()
		switch {
		case errors.Is(redisErr, redis.Nil):
			return CodexTurnRootBinding{}, false, nil
		case redisErr != nil:
			return CodexTurnRootBinding{}, false, redisErr
		case common.Unmarshal(payload, &binding) != nil:
			return CodexTurnRootBinding{}, false, ErrCodexTurnRootBindingInvalid
		default:
			found = true
		}
	} else {
		binding, found, err = cache.Get(key)
		if err != nil || !found {
			return CodexTurnRootBinding{}, found, err
		}
	}
	binding, valid := normalizeCodexTurnRootBinding(binding)
	if !valid {
		return CodexTurnRootBinding{}, false, ErrCodexTurnRootBindingInvalid
	}
	return binding, true, nil
}

// ResolveCodexTurnRootBinding revalidates the mapping against the current root
// channel/key and atomically refreshes that exact root's provisional lease.
func ResolveCodexTurnRootBinding(ctx context.Context, userID int, turnID string) (CodexTurnRootBinding, CodexRootChannelBinding, bool, error) {
	mapping, found, err := LoadCodexTurnRootBindingContext(ctx, userID, turnID)
	return resolveCodexLineageRootBinding(ctx, userID, mapping, found, err)
}

// ResolveCodexThreadRootBinding resolves a source Thread ID to its verified
// root route and rejects stale mappings after channel/key configuration changes.
func ResolveCodexThreadRootBinding(ctx context.Context, userID int, threadID string) (CodexTurnRootBinding, CodexRootChannelBinding, bool, error) {
	mapping, found, err := LoadCodexThreadRootBindingContext(ctx, userID, threadID)
	return resolveCodexLineageRootBinding(ctx, userID, mapping, found, err)
}

func resolveCodexLineageRootBinding(ctx context.Context, userID int, mapping CodexTurnRootBinding, found bool, err error) (CodexTurnRootBinding, CodexRootChannelBinding, bool, error) {
	if err != nil || !found {
		return CodexTurnRootBinding{}, CodexRootChannelBinding{}, found, err
	}
	rootBinding, rootFound, err := LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, mapping.RootID, mapping.UARoutingOnly)
	if err != nil {
		return CodexTurnRootBinding{}, CodexRootChannelBinding{}, true, err
	}
	if !rootFound || rootBinding.SelectedGroup != mapping.SelectedGroup ||
		CodexRootChannelBindingFingerprint(rootBinding) != mapping.BindingFingerprint {
		return CodexTurnRootBinding{}, CodexRootChannelBinding{}, true, ErrCodexTurnRootRouteUnavailable
	}
	winner, won, err := ClaimProvisionalCodexRootChannelBinding(userID, mapping.RootID, rootBinding)
	if err != nil {
		return CodexTurnRootBinding{}, CodexRootChannelBinding{}, true, err
	}
	if !won || winner != rootBinding {
		return CodexTurnRootBinding{}, CodexRootChannelBinding{}, true, ErrCodexTurnRootBindingConflict
	}
	return mapping, rootBinding, true, nil
}

func WaitForCodexTurnRootBindingUpdate(ctx context.Context, userID int, turnID string, maxWait time.Duration) error {
	return waitForCodexLineageRootBindingUpdate(ctx, codexTurnRootCacheKey(userID, turnID), maxWait)
}

func WaitForCodexThreadRootBindingUpdate(ctx context.Context, userID int, threadID string, maxWait time.Duration) error {
	return waitForCodexLineageRootBindingUpdate(ctx, codexThreadRootWaiterKey(userID, threadID), maxWait)
}

func waitForCodexLineageRootBindingUpdate(ctx context.Context, waiterKey string, maxWait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxWait <= 0 {
		return nil
	}
	if waiterKey == "" {
		return nil
	}
	codexTurnRootWaiters.Lock()
	waiter := codexTurnRootWaiters.items[waiterKey]
	if waiter == nil {
		waiter = &codexRootChannelWaiter{updates: make(chan struct{})}
		codexTurnRootWaiters.items[waiterKey] = waiter
	}
	waiter.count++
	codexTurnRootWaiters.Unlock()
	defer func() {
		codexTurnRootWaiters.Lock()
		if current := codexTurnRootWaiters.items[waiterKey]; current == waiter {
			current.count--
			if current.count == 0 {
				delete(codexTurnRootWaiters.items, waiterKey)
			}
		}
		codexTurnRootWaiters.Unlock()
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

func notifyCodexLineageRootBindingUpdate(waiterKey string) {
	if waiterKey == "" {
		return
	}
	codexTurnRootWaiters.Lock()
	waiter := codexTurnRootWaiters.items[waiterKey]
	if waiter != nil {
		delete(codexTurnRootWaiters.items, waiterKey)
		close(waiter.updates)
	}
	codexTurnRootWaiters.Unlock()
}
