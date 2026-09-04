package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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
	CodexUnlinkedAccountFallbackEnabledOptionKey = "codex_unlinked_account_fallback_enabled"
	CodexUnlinkedAccountFallbackSecondsOptionKey = "codex_unlinked_account_fallback_seconds"
	CodexUnlinkedAccountFallbackDefaultSeconds   = 300
	CodexUnlinkedAccountFallbackMaxSeconds       = 3600

	codexRecentRootChannelCandidateNamespace = "new-api:codex_recent_root_channel:v3"
	codexRootObservationNamespace            = "new-api:codex_root_observation:v1"
	codexRequestArrivalSequenceNamespace     = "new-api:codex_request_arrival:v1"
	codexPassiveRootAliasNamespace           = "new-api:codex_passive_root_alias:v2"
	codexTitleRootCandidateNamespace         = "new-api:codex_title_root_candidate:v1"
	codexRecentRootChannelCandidateTTL       = 30 * time.Second
	codexProvisionalRootCandidateTTL         = 2 * time.Minute
	// A scope container must live at least as long as its longest per-member
	// candidate. Member scores still enforce the shorter successful-root lifetime.
	codexRecentRootChannelContainerTTL   = codexProvisionalRootCandidateTTL
	codexPassiveRootAliasProvisionalTTL  = 2 * time.Minute
	codexPassiveRootAliasTTL             = 24 * time.Hour
	codexRecentRootChannelCandidateLimit = 32
	codexPassiveRootRedisTimeout         = 500 * time.Millisecond
	codexRecentRootPollInterval          = 200 * time.Millisecond
	codexTitleRootCandidateTTL           = 5 * time.Second
	codexRootObservationWindow           = 30 * time.Second
	// Keep observations long enough for the maximum operator-configured
	// fallback window. Individual lookups still enforce their requested age.
	codexRootObservationFallbackWindow = CodexUnlinkedAccountFallbackMaxSeconds * time.Second
	codexRootObservationContainerTTL     = codexRootObservationFallbackWindow
	codexRequestArrivalSequenceTTL       = 10 * time.Minute
)

// CodexUnlinkedAccountFallbackEnabled reads the persisted NewAPI option. The
// switch is intentionally opt-in; strict 30-second predecessor matching is
// unchanged when it is disabled.
func CodexUnlinkedAccountFallbackEnabled() bool {
	common.OptionMapRWMutex.RLock()
	raw, found := common.OptionMap[CodexUnlinkedAccountFallbackEnabledOptionKey]
	common.OptionMapRWMutex.RUnlock()
	return found && (strings.EqualFold(strings.TrimSpace(raw), "true") || strings.TrimSpace(raw) == "1")
}

func CodexUnlinkedAccountFallbackSeconds() int {
	common.OptionMapRWMutex.RLock()
	raw, found := common.OptionMap[CodexUnlinkedAccountFallbackSecondsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if !found || err != nil || seconds <= 0 {
		seconds = CodexUnlinkedAccountFallbackDefaultSeconds
	}
	if seconds > CodexUnlinkedAccountFallbackMaxSeconds {
		seconds = CodexUnlinkedAccountFallbackMaxSeconds
	}
	return seconds
}

func CodexUnlinkedAccountFallbackWindow() time.Duration {
	return time.Duration(CodexUnlinkedAccountFallbackSeconds()) * time.Second
}

var (
	ErrCodexPassiveRootAliasConflict     = errors.New("Codex passive root alias conflict")
	ErrCodexPassiveRootCandidatesChanged = errors.New("Codex passive root candidates changed before alias claim")
	ErrCodexPassiveRootAliasInvalid      = errors.New("invalid Codex passive root alias")
	ErrCodexRecentRootBindingUnavailable = errors.New("Codex recent root channel binding is unavailable")
)

type CodexRecentRootChannelCandidate struct {
	RootID             string
	Binding            CodexRootChannelBinding
	BindingFingerprint string
	ExpiresAt          time.Time
	ObservationID      string
	ArrivalOrder       int64
	ArrivedAt          time.Time
	// ObservationWindow records the maximum predecessor age proved when this
	// candidate was loaded. It is used by the atomic alias claim to apply the
	// same strict or extended window that selected the event.
	ObservationWindow time.Duration
}

// CodexRequestArrival is a server-issued ordering ticket for one downstream
// request. Order is monotonic within one platform/user/token/device scope;
// ArrivedAt is taken from the same backing store so the predecessor window is
// consistent across instances.
type CodexRequestArrival struct {
	Order     int64
	ArrivedAt time.Time
	scopeKey  string
}

// CodexPassiveRootScope is the identity namespace used by the bounded
// no-parent fallback.  It deliberately contains the same signed NewAPI
// identity components that Codex2API uses, while leaving the canonical
// root/session affinity key untouched.
type CodexPassiveRootScope struct {
	PlatformID     string
	UserID         int
	TokenID        int
	InstallationID string
}

func normalizeCodexPassiveRootScope(userID, tokenID int, scopes []CodexPassiveRootScope) CodexPassiveRootScope {
	scope := CodexPassiveRootScope{UserID: userID, TokenID: tokenID}
	if len(scopes) > 0 {
		scope = scopes[0]
		if scope.UserID <= 0 {
			scope.UserID = userID
		}
		if scope.TokenID <= 0 {
			scope.TokenID = tokenID
		}
	}
	scope.PlatformID = strings.ToLower(strings.TrimSpace(scope.PlatformID))
	if scope.PlatformID == "" {
		scope.PlatformID = strings.ToLower(strings.TrimSpace(common.GetEnvOrDefaultString("CODEX2API_POLICY_PLATFORM_ID", "newapi")))
	}
	scope.InstallationID = strings.TrimSpace(scope.InstallationID)
	return scope
}

func codexPassiveRootScopeKey(userID, tokenID int, scopes ...CodexPassiveRootScope) string {
	scope := normalizeCodexPassiveRootScope(userID, tokenID, scopes)
	return codexPassiveRootRedisScopeKeyForScope(scope)
}

type CodexPassiveRootAlias struct {
	RootID             string `json:"root_id"`
	SelectedGroup      string `json:"selected_group"`
	UARoutingOnly      bool   `json:"ua_routing_only"`
	BindingFingerprint string `json:"binding_fingerprint"`
	// Temporary marks aliases inferred through the bounded fallback window. It
	// prevents a retry that loads the provisional alias from promoting it into a
	// durable root binding.
	Temporary bool `json:"temporary,omitempty"`
}

type codexRecentRootWaiter struct {
	updates chan struct{}
	count   int
}

var (
	codexRecentRootMemoryOnce      sync.Once
	codexRecentRootMemory          *hot.HotCache[string, map[string]int64]
	codexRecentRootMemoryMu        sync.Mutex
	codexRootObservationMemoryOnce sync.Once
	codexRootObservationMemory     *hot.HotCache[string, map[string]int64]
	codexRequestArrivalMemoryOnce  sync.Once
	codexRequestArrivalMemory      *hot.HotCache[string, int64]

	codexPassiveRootAliasMemoryOnce sync.Once
	codexPassiveRootAliasMemory     *hot.HotCache[string, CodexPassiveRootAlias]
	codexTitleRootMemoryOnce        sync.Once
	codexTitleRootMemory            *hot.HotCache[string, map[string]int64]

	codexRecentRootWaiters = struct {
		sync.Mutex
		items map[string]*codexRecentRootWaiter
	}{items: make(map[string]*codexRecentRootWaiter)}
	codexTitleRootWaiters = struct {
		sync.Mutex
		items map[string]*codexRecentRootWaiter
	}{items: make(map[string]*codexRecentRootWaiter)}
)

var beginCodexRequestArrivalScript = redis.NewScript(`
local order = redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[1])
local redis_time = redis.call('TIME')
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
return { order, now_ms }
`)

var storeCodexRootObservationScript = redis.NewScript(`
local existing = redis.call('ZRANGEBYSCORE', KEYS[1], ARGV[2], ARGV[2])
for _, member in ipairs(existing) do
  if member == ARGV[1] then
    redis.call('EXPIRE', KEYS[1], ARGV[4])
    return 1
  end
end
if #existing > 0 then return -1 end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[1])
local count = redis.call('ZCARD', KEYS[1])
local max_observations = tonumber(ARGV[3])
if count > max_observations then
  redis.call('ZREMRANGEBYRANK', KEYS[1], 0, count - max_observations - 1)
end
redis.call('EXPIRE', KEYS[1], ARGV[4])
return 1
`)

var loadLatestCodexRootObservationScript = redis.NewScript(`
return redis.call('ZREVRANGEBYSCORE', KEYS[1], '(' .. ARGV[1], '-inf', 'WITHSCORES', 'LIMIT', 0, 1)
`)

var storeCodexRecentRootCandidateScript = redis.NewScript(`
local redis_time = redis.call('TIME')
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
local expires_at = now_ms + tonumber(ARGV[1])
local current_expires_at = redis.call('ZSCORE', KEYS[1], ARGV[2])
local replace_expiry = ARGV[5] == '1'
if replace_expiry or not current_expires_at or tonumber(current_expires_at) < expires_at then
  redis.call('ZADD', KEYS[1], expires_at, ARGV[2])
end
local count = redis.call('ZCARD', KEYS[1])
local max_candidates = tonumber(ARGV[3])
if count > max_candidates then
  redis.call('ZREMRANGEBYRANK', KEYS[1], 0, count - max_candidates - 1)
end
redis.call('EXPIRE', KEYS[1], ARGV[4])
return 1
`)

var loadCodexRecentRootCandidatesScript = redis.NewScript(`
local redis_time = redis.call('TIME')
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
return redis.call('ZRANGEBYSCORE', KEYS[1], '(' .. now_ms, '+inf', 'WITHSCORES')
`)

var removeStaleCodexRecentRootCandidatesScript = redis.NewScript(`
local removed = 0
for index = 1, #ARGV, 2 do
  local current_score = redis.call('ZSCORE', KEYS[1], ARGV[index])
  if current_score and tonumber(current_score) == tonumber(ARGV[index + 1]) then
    removed = removed + redis.call('ZREM', KEYS[1], ARGV[index])
  end
end
return removed
`)

var claimCodexPassiveRootAliasScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
	if current == ARGV[1] then
		return 1
	end
	return -1
end
local redis_time = redis.call('TIME')
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now_ms)
local candidates = redis.call('ZRANGEBYSCORE', KEYS[2], '(' .. now_ms, '+inf')
if #candidates ~= 1 or candidates[1] ~= ARGV[2] then
	return 0
end
if redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3], 'NX') then
	return 1
end
local claimed = redis.call('GET', KEYS[1])
if claimed == ARGV[1] then
	return 1
end
if claimed then
	return -1
end
return 0
`)

var claimCodexObservedPassiveRootAliasScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
  if current == ARGV[1] then return 1 end
  return -1
end
local latest = redis.call('ZREVRANGEBYSCORE', KEYS[2], '(' .. ARGV[4], '-inf')
local selected = nil
local minimum_arrived_ms = tonumber(ARGV[6]) - tonumber(ARGV[7])
for _, member in ipairs(latest) do
  local _, _, arrived_ms = string.find(member, '^[^.]+%.([^.]+)%.')
  if arrived_ms and tonumber(arrived_ms) >= minimum_arrived_ms then
    selected = member
    break
  end
end
local score = redis.call('ZSCORE', KEYS[2], ARGV[2])
if selected ~= ARGV[2] or not score or tonumber(score) ~= tonumber(ARGV[5]) then return 0 end
if redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3], 'NX') then return 1 end
local claimed = redis.call('GET', KEYS[1])
if claimed == ARGV[1] then return 1 end
if claimed then return -1 end
return 0
`)

var promoteCodexPassiveRootAliasScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current or current ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
return 1
`)

var claimCodexTitleRootAliasScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
  if current == ARGV[1] then return 1 end
  return -1
end
local redis_time = redis.call('TIME')
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
local count = 0
local candidate = nil
for index = 2, 3 do
  redis.call('ZREMRANGEBYSCORE', KEYS[index], '-inf', now_ms)
  redis.call('ZREMRANGEBYSCORE', KEYS[index + 2], '-inf', now_ms)
  local fresh = redis.call('ZRANGEBYSCORE', KEYS[index], '(' .. now_ms, '+inf')
  for _, member in ipairs(fresh) do
    if redis.call('ZSCORE', KEYS[index + 2], member) then
      count = count + 1
      candidate = member
    end
  end
end
if count ~= 1 or candidate ~= ARGV[2] then return 0 end
if redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3], 'NX') then return 1 end
local claimed = redis.call('GET', KEYS[1])
if claimed == ARGV[1] then return 1 end
if claimed then return -1 end
return 0
`)

func getCodexRecentRootMemory() *hot.HotCache[string, map[string]int64] {
	codexRecentRootMemoryOnce.Do(func() {
		codexRecentRootMemory = hot.NewHotCache[string, map[string]int64](hot.LRU, 100_000).
			WithTTL(codexRecentRootChannelContainerTTL).
			WithJanitor().
			Build()
	})
	return codexRecentRootMemory
}

func getCodexRootObservationMemory() *hot.HotCache[string, map[string]int64] {
	codexRootObservationMemoryOnce.Do(func() {
		codexRootObservationMemory = hot.NewHotCache[string, map[string]int64](hot.LRU, 100_000).
			WithTTL(codexRootObservationContainerTTL).
			WithJanitor().
			Build()
	})
	return codexRootObservationMemory
}

func getCodexRequestArrivalMemory() *hot.HotCache[string, int64] {
	codexRequestArrivalMemoryOnce.Do(func() {
		codexRequestArrivalMemory = hot.NewHotCache[string, int64](hot.LRU, 100_000).
			WithTTL(codexRequestArrivalSequenceTTL).
			WithJanitor().
			Build()
	})
	return codexRequestArrivalMemory
}

func getCodexPassiveRootAliasMemory() *hot.HotCache[string, CodexPassiveRootAlias] {
	codexPassiveRootAliasMemoryOnce.Do(func() {
		codexPassiveRootAliasMemory = hot.NewHotCache[string, CodexPassiveRootAlias](hot.LRU, 200_000).
			WithTTL(codexPassiveRootAliasTTL).
			WithJanitor().
			Build()
	})
	return codexPassiveRootAliasMemory
}

func getCodexTitleRootMemory() *hot.HotCache[string, map[string]int64] {
	codexTitleRootMemoryOnce.Do(func() {
		codexTitleRootMemory = hot.NewHotCache[string, map[string]int64](hot.LRU, 100_000).
			WithTTL(codexTitleRootCandidateTTL).
			WithJanitor().
			Build()
	})
	return codexTitleRootMemory
}

func codexRecentRootChannelScopeKey(userID, tokenID int, uaRoutingOnly bool, scopes ...CodexPassiveRootScope) string {
	baseScope := codexPassiveRootScopeKey(userID, tokenID, scopes...)
	if baseScope == "" {
		return ""
	}
	side := "normal"
	if uaRoutingOnly {
		side = "ua"
	}
	return baseScope + ":" + side
}

func codexRecentRootChannelRedisKey(scopeKey string) string {
	baseScope, _, _ := strings.Cut(scopeKey, ":")
	if baseScope == "" {
		return ""
	}
	return cachex.Namespace(codexRecentRootChannelCandidateNamespace).FullKey("{" + baseScope + "}:" + scopeKey)
}

func codexRequestArrivalSequenceRedisKey(scopeKey string) string {
	if scopeKey == "" {
		return ""
	}
	return cachex.Namespace(codexRequestArrivalSequenceNamespace).FullKey("{" + scopeKey + "}:sequence")
}

func codexRootObservationRedisKey(scopeKey string) string {
	baseScope, _, _ := strings.Cut(scopeKey, ":")
	if baseScope == "" {
		return ""
	}
	return cachex.Namespace(codexRootObservationNamespace).FullKey("{" + baseScope + "}:" + scopeKey)
}

func codexTitleRootCandidateRedisKey(scopeKey string) string {
	baseScope, _, _ := strings.Cut(scopeKey, ":")
	if baseScope == "" {
		return ""
	}
	return cachex.Namespace(codexTitleRootCandidateNamespace).FullKey("{" + baseScope + "}:" + scopeKey)
}

func codexPassiveRootRedisScopeKey(userID, tokenID int) string {
	return codexPassiveRootRedisScopeKeyForScope(CodexPassiveRootScope{UserID: userID, TokenID: tokenID})
}

func codexPassiveRootRedisScopeKeyForScope(scope CodexPassiveRootScope) string {
	if scope.UserID <= 0 || scope.TokenID <= 0 {
		return ""
	}
	platform := strings.ToLower(strings.TrimSpace(scope.PlatformID))
	if platform == "" {
		platform = strings.ToLower(strings.TrimSpace(common.GetEnvOrDefaultString("CODEX2API_POLICY_PLATFORM_ID", "newapi")))
	}
	canonical := strings.Join([]string{
		"codex-passive-root-scope-v2",
		platform,
		strconv.Itoa(scope.UserID),
		strconv.Itoa(scope.TokenID),
		strings.TrimSpace(scope.InstallationID),
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func codexPassiveRootAliasCacheKey(userID, tokenID int, systemRootID string) string {
	return codexPassiveRootAliasCacheKeyForScope(CodexPassiveRootScope{UserID: userID, TokenID: tokenID}, systemRootID)
}

func codexPassiveRootAliasCacheKeyForScope(scope CodexPassiveRootScope, systemRootID string) string {
	systemRootID = strings.TrimSpace(systemRootID)
	if scope.UserID <= 0 || scope.TokenID <= 0 || systemRootID == "" {
		return ""
	}
	platform := strings.ToLower(strings.TrimSpace(scope.PlatformID))
	if platform == "" {
		platform = strings.ToLower(strings.TrimSpace(common.GetEnvOrDefaultString("CODEX2API_POLICY_PLATFORM_ID", "newapi")))
	}
	canonical := strings.Join([]string{
		"codex-passive-root-alias-v2",
		platform,
		strconv.Itoa(scope.UserID),
		strconv.Itoa(scope.TokenID),
		strings.TrimSpace(scope.InstallationID),
		systemRootID,
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func codexPassiveRootAliasRedisKey(scopeKey, cacheKey string) string {
	if scopeKey == "" || cacheKey == "" {
		return ""
	}
	return cachex.Namespace(codexPassiveRootAliasNamespace).FullKey("{" + scopeKey + "}:" + cacheKey)
}

// BeginCodexRequestArrival reserves the request's position before distributor
// parsing, waiting, or channel selection. The ticket is intentionally separate
// from ContextKeyRequestStartTime, which measures relay/FRT timing later.
func BeginCodexRequestArrival(ctx context.Context, userID, tokenID int, scopes ...CodexPassiveRootScope) (CodexRequestArrival, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CodexRequestArrival{}, err
	}
	scopeKey := codexPassiveRootScopeKey(userID, tokenID, scopes...)
	if scopeKey == "" {
		return CodexRequestArrival{}, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		arrivalContext, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		raw, err := beginCodexRequestArrivalScript.Run(arrivalContext, common.RDB,
			[]string{codexRequestArrivalSequenceRedisKey(scopeKey)},
			int64(codexRequestArrivalSequenceTTL/time.Millisecond)).Result()
		if err != nil {
			return CodexRequestArrival{}, err
		}
		values, ok := raw.([]interface{})
		if !ok || len(values) != 2 {
			return CodexRequestArrival{}, errors.New("invalid Codex request arrival response")
		}
		order, orderErr := strconv.ParseInt(redisResultString(values[0]), 10, 64)
		arrivedAtMillis, timeErr := strconv.ParseInt(redisResultString(values[1]), 10, 64)
		if orderErr != nil || timeErr != nil || order <= 0 || arrivedAtMillis <= 0 {
			return CodexRequestArrival{}, errors.New("invalid Codex request arrival values")
		}
		return CodexRequestArrival{
			Order: order, ArrivedAt: time.UnixMilli(arrivedAtMillis).UTC(), scopeKey: scopeKey,
		}, nil
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	sequenceCache := getCodexRequestArrivalMemory()
	current, found, err := sequenceCache.Get(scopeKey)
	if err != nil {
		return CodexRequestArrival{}, err
	}
	if !found || current < 0 {
		current = 0
	}
	current++
	sequenceCache.SetWithTTL(scopeKey, current, codexRequestArrivalSequenceTTL)
	return CodexRequestArrival{Order: current, ArrivedAt: time.Now().UTC(), scopeKey: scopeKey}, nil
}

func (arrival CodexRequestArrival) validFor(userID, tokenID int, scopes ...CodexPassiveRootScope) bool {
	return arrival.Order > 0 && !arrival.ArrivedAt.IsZero() &&
		arrival.scopeKey != "" && arrival.scopeKey == codexPassiveRootScopeKey(userID, tokenID, scopes...)
}

func codexRootObservationMember(arrival CodexRequestArrival, rootID, bindingFingerprint string) string {
	rootID = strings.TrimSpace(rootID)
	bindingFingerprint = strings.TrimSpace(bindingFingerprint)
	if arrival.Order <= 0 || arrival.ArrivedAt.IsZero() || rootID == "" || bindingFingerprint == "" {
		return ""
	}
	return strconv.FormatInt(arrival.Order, 10) + "." +
		strconv.FormatInt(arrival.ArrivedAt.UTC().UnixMilli(), 10) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(rootID)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(bindingFingerprint))
}

func parseCodexRootObservationMember(member string) (CodexRequestArrival, string, string, bool) {
	parts := strings.Split(member, ".")
	if len(parts) != 4 {
		return CodexRequestArrival{}, "", "", false
	}
	order, orderErr := strconv.ParseInt(parts[0], 10, 64)
	arrivedAtMillis, timeErr := strconv.ParseInt(parts[1], 10, 64)
	rootIDBytes, rootErr := base64.RawURLEncoding.DecodeString(parts[2])
	fingerprintBytes, fingerprintErr := base64.RawURLEncoding.DecodeString(parts[3])
	arrival := CodexRequestArrival{Order: order, ArrivedAt: time.UnixMilli(arrivedAtMillis).UTC()}
	rootID := strings.TrimSpace(string(rootIDBytes))
	bindingFingerprint := strings.TrimSpace(string(fingerprintBytes))
	if orderErr != nil || timeErr != nil || rootErr != nil || fingerprintErr != nil ||
		order <= 0 || arrivedAtMillis <= 0 || rootID == "" || bindingFingerprint == "" ||
		codexRootObservationMember(arrival, rootID, bindingFingerprint) != member {
		return CodexRequestArrival{}, "", "", false
	}
	return arrival, rootID, bindingFingerprint, true
}

// StoreCodexRootChannelObservation publishes one root request using the ticket
// reserved at HTTP arrival. Provisional and successful publication of the same
// request therefore update the same event, while a later request on the same
// root gets a distinct order and cannot overwrite its predecessor.
func StoreCodexRootChannelObservation(userID, tokenID int, rootID string, binding CodexRootChannelBinding, arrival CodexRequestArrival, scopes ...CodexPassiveRootScope) error {
	rootID = strings.TrimSpace(rootID)
	bindingFingerprint := CodexRootChannelBindingFingerprint(binding)
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly, scopes...)
	member := codexRootObservationMember(arrival, rootID, bindingFingerprint)
	if scopeKey == "" || member == "" || !arrival.validFor(userID, tokenID, scopes...) {
		return ErrCodexRecentRootBindingUnavailable
	}
	currentBinding, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, binding.UARoutingOnly)
	if err != nil {
		return err
	}
	if !found || currentBinding.UARoutingOnly != binding.UARoutingOnly ||
		CodexRootChannelBindingFingerprint(currentBinding) != bindingFingerprint {
		return ErrCodexRecentRootBindingUnavailable
	}
	if common.RedisEnabled && common.RDB != nil {
		storeContext, cancel := context.WithTimeout(context.Background(), codexPassiveRootRedisTimeout)
		defer cancel()
		stored, err := storeCodexRootObservationScript.Run(storeContext, common.RDB,
			[]string{codexRootObservationRedisKey(scopeKey)}, member, arrival.Order,
			codexRecentRootChannelCandidateLimit, int64(codexRootObservationContainerTTL/time.Second)).Int()
		if err != nil {
			return err
		}
		if stored < 0 {
			return ErrCodexPassiveRootCandidatesChanged
		}
	} else {
		codexRecentRootMemoryMu.Lock()
		cache := getCodexRootObservationMemory()
		current, currentFound, cacheErr := cache.Get(scopeKey)
		if cacheErr != nil {
			codexRecentRootMemoryMu.Unlock()
			return cacheErr
		}
		updated := make(map[string]int64, len(current)+1)
		if currentFound {
			for eventID, order := range current {
				if order == arrival.Order && eventID != member {
					codexRecentRootMemoryMu.Unlock()
					return ErrCodexPassiveRootCandidatesChanged
				}
				updated[eventID] = order
			}
		}
		updated[member] = arrival.Order
		for len(updated) > codexRecentRootChannelCandidateLimit {
			oldestEvent := ""
			oldestOrder := int64(0)
			for eventID, order := range updated {
				if oldestEvent == "" || order < oldestOrder || (order == oldestOrder && eventID < oldestEvent) {
					oldestEvent = eventID
					oldestOrder = order
				}
			}
			delete(updated, oldestEvent)
		}
		cache.SetWithTTL(scopeKey, updated, codexRootObservationContainerTTL)
		codexRecentRootMemoryMu.Unlock()
	}
	notifyCodexRecentRootChannelUpdate(scopeKey)
	return nil
}

// LoadLatestCodexRootChannelObservationBefore returns the immediate published
// predecessor on one routing side, provided it arrived no more than 30 seconds
// before cutoff. Events at or after cutoff are invisible even if they publish
// while this request is waiting.
func LoadLatestCodexRootChannelObservationBefore(ctx context.Context, userID, tokenID int, uaRoutingOnly bool, cutoff CodexRequestArrival, scopes ...CodexPassiveRootScope) (CodexRecentRootChannelCandidate, bool, error) {
	return LoadLatestCodexRootChannelObservationBeforeWithin(ctx, userID, tokenID, uaRoutingOnly, cutoff, codexRootObservationWindow, scopes...)
}

// LoadLatestCodexRootChannelObservationBeforeWithin is the bounded fallback
// variant used when the strict 30-second predecessor window has no event. It
// still returns only the immediate predecessor before the server-issued
// arrival ticket; a later request can never be used to infer this request's
// parent. Callers should keep the window finite and treat the result as a
// lower-confidence alias.
func LoadLatestCodexRootChannelObservationBeforeWithin(ctx context.Context, userID, tokenID int, uaRoutingOnly bool, cutoff CodexRequestArrival, observationWindow time.Duration, scopes ...CodexPassiveRootScope) (CodexRecentRootChannelCandidate, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CodexRecentRootChannelCandidate{}, false, err
	}
	if observationWindow <= 0 {
		observationWindow = codexRootObservationWindow
	}
	if !cutoff.validFor(userID, tokenID, scopes...) {
		return CodexRecentRootChannelCandidate{}, false, ErrCodexRecentRootBindingUnavailable
	}
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, uaRoutingOnly, scopes...)
	if scopeKey == "" {
		return CodexRecentRootChannelCandidate{}, false, nil
	}
	eventID := ""
	eventOrder := int64(0)
	if common.RedisEnabled && common.RDB != nil {
		loadContext, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		raw, err := loadLatestCodexRootObservationScript.Run(loadContext, common.RDB,
			[]string{codexRootObservationRedisKey(scopeKey)}, cutoff.Order).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return CodexRecentRootChannelCandidate{}, false, err
		}
		values, ok := raw.([]interface{})
		if !ok || len(values) == 0 {
			return CodexRecentRootChannelCandidate{}, false, nil
		}
		if len(values) != 2 {
			return CodexRecentRootChannelCandidate{}, false, errors.New("invalid Codex root observation response")
		}
		eventID = redisResultString(values[0])
		parsedOrder, parseErr := strconv.ParseFloat(redisResultString(values[1]), 64)
		if parseErr != nil {
			return CodexRecentRootChannelCandidate{}, false, errors.New("invalid Codex root observation score")
		}
		eventOrder = int64(parsedOrder)
	} else {
		codexRecentRootMemoryMu.Lock()
		current, found, err := getCodexRootObservationMemory().Get(scopeKey)
		if err != nil {
			codexRecentRootMemoryMu.Unlock()
			return CodexRecentRootChannelCandidate{}, false, err
		}
		if found {
			for candidateEvent, order := range current {
				if order >= cutoff.Order {
					continue
				}
				if eventID == "" || order > eventOrder || (order == eventOrder && candidateEvent < eventID) {
					eventID = candidateEvent
					eventOrder = order
				}
			}
		}
		codexRecentRootMemoryMu.Unlock()
		if eventID == "" {
			return CodexRecentRootChannelCandidate{}, false, nil
		}
	}
	arrival, rootID, bindingFingerprint, validEvent := parseCodexRootObservationMember(eventID)
	if !validEvent || arrival.Order != eventOrder || arrival.Order >= cutoff.Order || arrival.ArrivedAt.After(cutoff.ArrivedAt) {
		return CodexRecentRootChannelCandidate{}, false, ErrCodexPassiveRootCandidatesChanged
	}
	if arrival.ArrivedAt.Before(cutoff.ArrivedAt.Add(-observationWindow)) {
		return CodexRecentRootChannelCandidate{}, false, nil
	}
	binding, found, err := LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, rootID, uaRoutingOnly)
	if err != nil {
		return CodexRecentRootChannelCandidate{}, false, err
	}
	if !found || binding.UARoutingOnly != uaRoutingOnly || CodexRootChannelBindingFingerprint(binding) != bindingFingerprint {
		return CodexRecentRootChannelCandidate{}, false, ErrCodexRecentRootBindingUnavailable
	}
	return CodexRecentRootChannelCandidate{
		RootID: rootID, Binding: binding, BindingFingerprint: bindingFingerprint,
		ExpiresAt: arrival.ArrivedAt.Add(observationWindow), ObservationID: eventID,
		ArrivalOrder: arrival.Order, ArrivedAt: arrival.ArrivedAt, ObservationWindow: observationWindow,
	}, true, nil
}

func StoreRecentCodexRootChannelBinding(userID, tokenID int, rootID string, binding CodexRootChannelBinding, scopes ...CodexPassiveRootScope) error {
	rootID = strings.TrimSpace(rootID)
	if codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly, scopes...) == "" ||
		codexRecentRootCandidateMember(rootID, CodexRootChannelBindingFingerprint(binding)) == "" {
		return nil
	}
	if err := StoreCodexRootChannelBinding(userID, rootID, binding); err != nil {
		return err
	}
	return StoreRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, scopes...)
}

func StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID int, rootID string, binding CodexRootChannelBinding, scopes ...CodexPassiveRootScope) error {
	rootID = strings.TrimSpace(rootID)
	if codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly, scopes...) == "" ||
		codexRecentRootCandidateMember(rootID, CodexRootChannelBindingFingerprint(binding)) == "" {
		return nil
	}
	winner, selectedWon, err := ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
	if err != nil {
		return err
	}
	if !selectedWon {
		return ErrCodexRootChannelBindingConflict
	}
	return StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, winner, scopes...)
}

// StoreRecentCodexRootChannelCandidate records an already-persisted successful
// root binding as a short-lived passive-route candidate.
func StoreRecentCodexRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding, scopes ...CodexPassiveRootScope) error {
	return storeValidatedRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, codexRecentRootChannelCandidateTTL, true, scopes...)
}

// StoreProvisionalRecentCodexRootChannelCandidate records an already-claimed
// in-flight root binding and only extends an existing candidate expiry.
func StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding, scopes ...CodexPassiveRootScope) error {
	return storeValidatedRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, codexProvisionalRootCandidateTTL, false, scopes...)
}

// StoreProvisionalCodexTitleRootChannelCandidate marks one already-published
// recent root as eligible for a title request arriving in the next few seconds.
func StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding, scopes ...CodexPassiveRootScope) error {
	rootID = strings.TrimSpace(rootID)
	fingerprint := CodexRootChannelBindingFingerprint(binding)
	member := codexRecentRootCandidateMember(rootID, fingerprint)
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly, scopes...)
	if scopeKey == "" || member == "" {
		return nil
	}
	candidates, err := LoadRecentCodexRootChannelCandidates(context.Background(), userID, tokenID, binding.UARoutingOnly, scopes...)
	if err != nil {
		return err
	}
	eligible := false
	for _, candidate := range candidates {
		if candidate.RootID == rootID && candidate.BindingFingerprint == fingerprint {
			eligible = true
			break
		}
	}
	if !eligible {
		return ErrCodexRecentRootBindingUnavailable
	}
	expiresAt := time.Now().UTC().Add(codexTitleRootCandidateTTL)
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), codexPassiveRootRedisTimeout)
		defer cancel()
		_, err = storeCodexRecentRootCandidateScript.Run(ctx, common.RDB,
			[]string{codexTitleRootCandidateRedisKey(scopeKey)}, int64(codexTitleRootCandidateTTL/time.Millisecond),
			member, codexRecentRootChannelCandidateLimit, int64(codexTitleRootCandidateTTL/time.Second), 0).Result()
		if err != nil {
			return err
		}
	} else {
		codexRecentRootMemoryMu.Lock()
		storeCodexCandidateMemory(getCodexTitleRootMemory(), scopeKey, member, expiresAt, codexTitleRootCandidateTTL)
		codexRecentRootMemoryMu.Unlock()
	}
	notifyCodexTitleRootChannelUpdate(codexPassiveRootScopeKey(userID, tokenID, scopes...))
	return nil
}

func storeCodexCandidateMemory(cache *hot.HotCache[string, map[string]int64], scopeKey, member string, expiresAt time.Time, ttl time.Duration) {
	current, found, _ := cache.Get(scopeKey)
	updated := make(map[string]int64, len(current)+1)
	nowMillis := time.Now().UTC().UnixMilli()
	if found {
		for candidate, candidateExpiresAt := range current {
			if candidateExpiresAt > nowMillis {
				updated[candidate] = candidateExpiresAt
			}
		}
	}
	if previous := updated[member]; previous < expiresAt.UnixMilli() {
		updated[member] = expiresAt.UnixMilli()
	}
	cache.SetWithTTL(scopeKey, updated, ttl)
}

func storeValidatedRecentCodexRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding, activeTTL time.Duration, replaceExpiry bool, scopes ...CodexPassiveRootScope) error {
	rootID = strings.TrimSpace(rootID)
	bindingFingerprint := CodexRootChannelBindingFingerprint(binding)
	candidateMember := codexRecentRootCandidateMember(rootID, bindingFingerprint)
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly, scopes...)
	if scopeKey == "" || candidateMember == "" || activeTTL <= 0 {
		return nil
	}
	currentBinding, found, err := LoadCodexRootChannelBindingForRoutingSide(userID, rootID, binding.UARoutingOnly)
	if err != nil {
		return err
	}
	if !found || currentBinding.UARoutingOnly != binding.UARoutingOnly ||
		CodexRootChannelBindingFingerprint(currentBinding) != bindingFingerprint {
		return ErrCodexRecentRootBindingUnavailable
	}
	now := time.Now().UTC()
	if common.RedisEnabled && common.RDB != nil {
		replaceExpiryFlag := 0
		if replaceExpiry {
			replaceExpiryFlag = 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), codexPassiveRootRedisTimeout)
		defer cancel()
		_, err = storeCodexRecentRootCandidateScript.Run(ctx, common.RDB, []string{codexRecentRootChannelRedisKey(scopeKey)},
			int64(activeTTL/time.Millisecond), candidateMember,
			codexRecentRootChannelCandidateLimit, int64(codexRecentRootChannelContainerTTL/time.Second),
			replaceExpiryFlag).Result()
		if err != nil {
			return err
		}
	} else {
		storeCodexRecentRootCandidateMemory(scopeKey, candidateMember, now.Add(activeTTL), replaceExpiry)
	}
	notifyCodexRecentRootChannelUpdate(scopeKey)
	return nil
}

func codexRecentRootCandidateMember(rootID, bindingFingerprint string) string {
	rootID = strings.TrimSpace(rootID)
	bindingFingerprint = strings.TrimSpace(bindingFingerprint)
	if rootID == "" || bindingFingerprint == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(rootID)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(bindingFingerprint))
}

func parseCodexRecentRootCandidateMember(member string) (string, string, bool) {
	encodedRootID, encodedFingerprint, found := strings.Cut(member, ".")
	if !found || encodedRootID == "" || encodedFingerprint == "" || strings.Contains(encodedFingerprint, ".") {
		return "", "", false
	}
	rootIDBytes, rootErr := base64.RawURLEncoding.DecodeString(encodedRootID)
	fingerprintBytes, fingerprintErr := base64.RawURLEncoding.DecodeString(encodedFingerprint)
	if rootErr != nil || fingerprintErr != nil {
		return "", "", false
	}
	rootID := strings.TrimSpace(string(rootIDBytes))
	bindingFingerprint := strings.TrimSpace(string(fingerprintBytes))
	if rootID == "" || bindingFingerprint == "" || codexRecentRootCandidateMember(rootID, bindingFingerprint) != member {
		return "", "", false
	}
	return rootID, bindingFingerprint, true
}

func storeCodexRecentRootCandidateMemory(scopeKey, candidateMember string, expiresAt time.Time, replaceExpiry bool) {
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	cache := getCodexRecentRootMemory()
	current, found, _ := cache.Get(scopeKey)
	if !found {
		current = make(map[string]int64)
	} else {
		cloned := make(map[string]int64, len(current)+1)
		for candidateRoot, seenAt := range current {
			cloned[candidateRoot] = seenAt
		}
		current = cloned
	}
	nowMillis := time.Now().UTC().UnixMilli()
	for member, candidateExpiresAt := range current {
		if candidateExpiresAt <= nowMillis {
			delete(current, member)
		}
	}
	newExpiresAt := expiresAt.UnixMilli()
	if currentExpiresAt, exists := current[candidateMember]; replaceExpiry || !exists || currentExpiresAt < newExpiresAt {
		current[candidateMember] = newExpiresAt
	}
	for len(current) > codexRecentRootChannelCandidateLimit {
		oldestRoot := ""
		oldestSeenAt := int64(0)
		for member, candidateExpiresAt := range current {
			if oldestRoot == "" || candidateExpiresAt < oldestSeenAt || (candidateExpiresAt == oldestSeenAt && member < oldestRoot) {
				oldestRoot = member
				oldestSeenAt = candidateExpiresAt
			}
		}
		delete(current, oldestRoot)
	}
	cache.SetWithTTL(scopeKey, current, codexRecentRootChannelContainerTTL)
}

func LoadRecentCodexRootChannelCandidates(ctx context.Context, userID, tokenID int, uaRoutingOnly bool, scopes ...CodexPassiveRootScope) ([]CodexRecentRootChannelCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, uaRoutingOnly, scopes...)
	if scopeKey == "" {
		return nil, nil
	}
	loadContext, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	defer cancel()
	now := time.Now().UTC()
	expiresByMember, err := loadCodexRecentRootCandidateTimes(loadContext, scopeKey, now)
	if err != nil {
		return nil, err
	}
	type candidateReference struct {
		member             string
		rootID             string
		bindingFingerprint string
		expiresAt          int64
	}
	references := make([]candidateReference, 0, len(expiresByMember))
	staleMembers := make(map[string]int64)
	for member, expiresAt := range expiresByMember {
		rootID, bindingFingerprint, validMember := parseCodexRecentRootCandidateMember(member)
		if !validMember {
			staleMembers[member] = expiresAt
			continue
		}
		references = append(references, candidateReference{
			member: member, rootID: rootID, bindingFingerprint: bindingFingerprint, expiresAt: expiresAt,
		})
	}

	bindings := make([]CodexRootChannelBinding, len(references))
	foundBindings := make([]bool, len(references))
	if common.RedisEnabled && common.RDB != nil && len(references) > 0 {
		pipeline := common.RDB.Pipeline()
		v2Commands := make([]*redis.StringCmd, len(references))
		legacyCommands := make([]*redis.StringCmd, len(references))
		for index, reference := range references {
			v2Commands[index] = pipeline.Get(loadContext, getCodexRootChannelCache().FullKey(
				codexRootChannelCacheKey(userID, reference.rootID, uaRoutingOnly),
			))
			legacyCommands[index] = pipeline.Get(loadContext, getCodexLegacyRootChannelCache().FullKey(
				legacyCodexRootChannelCacheKey(userID, reference.rootID),
			))
		}
		// Inspect each GET below instead of returning Pipeline.Exec's first error.
		// A stale/corrupt legacy key must not fail a request when its v2 value is
		// present and authoritative.
		_, _ = pipeline.Exec(loadContext)
		for index := range references {
			payload, getErr := v2Commands[index].Bytes()
			if errors.Is(getErr, redis.Nil) {
				payload, getErr = legacyCommands[index].Bytes()
			}
			if errors.Is(getErr, redis.Nil) {
				continue
			}
			if getErr != nil {
				return nil, getErr
			}
			if err := common.Unmarshal(payload, &bindings[index]); err != nil {
				return nil, err
			}
			foundBindings[index] = true
		}
	} else {
		for index, reference := range references {
			binding, found, loadErr := LoadCodexRootChannelBindingForRoutingSideContext(loadContext, userID, reference.rootID, uaRoutingOnly)
			if loadErr != nil {
				return nil, loadErr
			}
			bindings[index] = binding
			foundBindings[index] = found
		}
	}

	candidates := make([]CodexRecentRootChannelCandidate, 0, len(references))
	for index, reference := range references {
		binding := bindings[index]
		if !foundBindings[index] || binding.UARoutingOnly != uaRoutingOnly ||
			CodexRootChannelBindingFingerprint(binding) != reference.bindingFingerprint {
			staleMembers[reference.member] = reference.expiresAt
			continue
		}
		candidates = append(candidates, CodexRecentRootChannelCandidate{
			RootID: reference.rootID, Binding: binding, BindingFingerprint: reference.bindingFingerprint,
			ExpiresAt: time.UnixMilli(reference.expiresAt).UTC(),
		})
	}
	if err := removeStaleCodexRecentRootCandidates(loadContext, scopeKey, staleMembers); err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ExpiresAt.Equal(candidates[j].ExpiresAt) {
			return candidates[i].RootID < candidates[j].RootID
		}
		return candidates[i].ExpiresAt.After(candidates[j].ExpiresAt)
	})
	return candidates, nil
}

// LoadCodexTitleRootChannelCandidates returns only roots present in both the
// normal recent-candidate set and the five-second title marker, across both
// routing sides.
func LoadCodexTitleRootChannelCandidates(ctx context.Context, userID, tokenID int, scopes ...CodexPassiveRootScope) ([]CodexRecentRootChannelCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]CodexRecentRootChannelCandidate, 0, 2)
	for _, uaRoutingOnly := range []bool{false, true} {
		recent, err := LoadRecentCodexRootChannelCandidates(ctx, userID, tokenID, uaRoutingOnly, scopes...)
		if err != nil {
			return nil, err
		}
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, uaRoutingOnly, scopes...)
		fresh, err := loadCodexCandidateTimes(ctx, scopeKey, codexTitleRootCandidateRedisKey(scopeKey), getCodexTitleRootMemory(), time.Now().UTC())
		if err != nil {
			return nil, err
		}
		for _, candidate := range recent {
			member := codexRecentRootCandidateMember(candidate.RootID, candidate.BindingFingerprint)
			if expiresAt, found := fresh[member]; found {
				candidate.ExpiresAt = time.UnixMilli(expiresAt).UTC()
				result = append(result, candidate)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ExpiresAt.Equal(result[j].ExpiresAt) {
			return result[i].RootID < result[j].RootID
		}
		return result[i].ExpiresAt.After(result[j].ExpiresAt)
	})
	return result, nil
}

func loadCodexCandidateTimes(ctx context.Context, scopeKey, redisKey string, memory *hot.HotCache[string, map[string]int64], now time.Time) (map[string]int64, error) {
	if scopeKey == "" {
		return map[string]int64{}, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		loadContext, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		raw, err := loadCodexRecentRootCandidatesScript.Run(loadContext, common.RDB, []string{redisKey}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		return decodeCodexRecentRootCandidateScores(raw)
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	current, found, err := memory.Get(scopeKey)
	if err != nil || !found {
		return nil, err
	}
	active := make(map[string]int64, len(current))
	for member, expiresAt := range current {
		if expiresAt > now.UnixMilli() {
			active[member] = expiresAt
		}
	}
	if len(active) == 0 {
		memory.Delete(scopeKey)
	}
	return active, nil
}

func removeStaleCodexRecentRootCandidates(ctx context.Context, scopeKey string, staleMembers map[string]int64) error {
	if len(staleMembers) == 0 {
		return nil
	}
	if common.RedisEnabled && common.RDB != nil {
		arguments := make([]any, 0, len(staleMembers)*2)
		members := make([]string, 0, len(staleMembers))
		for member := range staleMembers {
			members = append(members, member)
		}
		sort.Strings(members)
		for _, member := range members {
			arguments = append(arguments, member, staleMembers[member])
		}
		_, err := removeStaleCodexRecentRootCandidatesScript.Run(ctx, common.RDB,
			[]string{codexRecentRootChannelRedisKey(scopeKey)}, arguments...).Result()
		return err
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	cache := getCodexRecentRootMemory()
	current, found, err := cache.Get(scopeKey)
	if err != nil || !found {
		return err
	}
	updated := make(map[string]int64, len(current))
	for member, expiresAt := range current {
		if staleScore, stale := staleMembers[member]; stale && staleScore == expiresAt {
			continue
		}
		updated[member] = expiresAt
	}
	if len(updated) == 0 {
		cache.Delete(scopeKey)
	} else if len(updated) != len(current) {
		cache.SetWithTTL(scopeKey, updated, codexRecentRootChannelContainerTTL)
	}
	return nil
}

func loadCodexRecentRootCandidateTimes(ctx context.Context, scopeKey string, now time.Time) (map[string]int64, error) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		raw, err := loadCodexRecentRootCandidatesScript.Run(ctx, common.RDB, []string{codexRecentRootChannelRedisKey(scopeKey)}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		return decodeCodexRecentRootCandidateScores(raw)
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	cache := getCodexRecentRootMemory()
	current, found, err := cache.Get(scopeKey)
	if err != nil || !found {
		return nil, err
	}
	nowMillis := now.UnixMilli()
	active := make(map[string]int64, len(current))
	for member, expiresAt := range current {
		if expiresAt > nowMillis {
			active[member] = expiresAt
		}
	}
	if len(active) == 0 {
		cache.Delete(scopeKey)
	} else if len(active) != len(current) {
		cache.SetWithTTL(scopeKey, active, codexRecentRootChannelContainerTTL)
	}
	return active, nil
}

func decodeCodexRecentRootCandidateScores(raw any) (map[string]int64, error) {
	if raw == nil {
		return map[string]int64{}, nil
	}
	values, ok := raw.([]interface{})
	if !ok || len(values)%2 != 0 {
		return nil, fmt.Errorf("invalid Codex recent root candidate response")
	}
	result := make(map[string]int64, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		rootID := redisResultString(values[index])
		scoreText := redisResultString(values[index+1])
		score, err := strconv.ParseFloat(scoreText, 64)
		if rootID == "" || err != nil {
			return nil, fmt.Errorf("invalid Codex recent root candidate entry")
		}
		result[rootID] = int64(score)
	}
	return result, nil
}

func redisResultString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

func WaitForRecentCodexRootChannelUpdate(ctx context.Context, userID, tokenID int, uaRoutingOnly bool, maxWait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxWait <= 0 {
		return nil
	}
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, uaRoutingOnly)
	if scopeKey == "" {
		return nil
	}
	codexRecentRootWaiters.Lock()
	waiter := codexRecentRootWaiters.items[scopeKey]
	if waiter == nil {
		waiter = &codexRecentRootWaiter{updates: make(chan struct{})}
		codexRecentRootWaiters.items[scopeKey] = waiter
	}
	waiter.count++
	codexRecentRootWaiters.Unlock()
	defer func() {
		codexRecentRootWaiters.Lock()
		if current := codexRecentRootWaiters.items[scopeKey]; current == waiter {
			current.count--
			if current.count == 0 {
				delete(codexRecentRootWaiters.items, scopeKey)
			}
		}
		codexRecentRootWaiters.Unlock()
	}()
	waitDuration := maxWait
	if waitDuration > codexRecentRootPollInterval {
		waitDuration = codexRecentRootPollInterval
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

func notifyCodexRecentRootChannelUpdate(scopeKey string) {
	codexRecentRootWaiters.Lock()
	waiter := codexRecentRootWaiters.items[scopeKey]
	if waiter != nil {
		delete(codexRecentRootWaiters.items, scopeKey)
		close(waiter.updates)
	}
	codexRecentRootWaiters.Unlock()
}

func WaitForCodexTitleRootChannelUpdate(ctx context.Context, userID, tokenID int, maxWait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxWait <= 0 {
		return nil
	}
	scopeKey := codexPassiveRootScopeKey(userID, tokenID)
	if scopeKey == "" {
		return nil
	}
	codexTitleRootWaiters.Lock()
	waiter := codexTitleRootWaiters.items[scopeKey]
	if waiter == nil {
		waiter = &codexRecentRootWaiter{updates: make(chan struct{})}
		codexTitleRootWaiters.items[scopeKey] = waiter
	}
	waiter.count++
	codexTitleRootWaiters.Unlock()
	defer func() {
		codexTitleRootWaiters.Lock()
		if current := codexTitleRootWaiters.items[scopeKey]; current == waiter {
			current.count--
			if current.count == 0 {
				delete(codexTitleRootWaiters.items, scopeKey)
			}
		}
		codexTitleRootWaiters.Unlock()
	}()
	waitDuration := maxWait
	if waitDuration > codexRecentRootPollInterval {
		waitDuration = codexRecentRootPollInterval
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

func notifyCodexTitleRootChannelUpdate(scopeKey string) {
	codexTitleRootWaiters.Lock()
	waiter := codexTitleRootWaiters.items[scopeKey]
	if waiter != nil {
		delete(codexTitleRootWaiters.items, scopeKey)
		close(waiter.updates)
	}
	codexTitleRootWaiters.Unlock()
}

// ClaimCodexTitleRootAlias atomically binds an unlinked title session only
// when the fresh-title/recent-candidate intersection contains exactly the
// requested root across both routing sides.
func ClaimCodexTitleRootAlias(ctx context.Context, userID, tokenID int, titleRootID string, alias CodexPassiveRootAlias, scopes ...CodexPassiveRootScope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope := normalizeCodexPassiveRootScope(userID, tokenID, scopes)
	cacheKey := codexPassiveRootAliasCacheKeyForScope(scope, titleRootID)
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	if cacheKey == "" || !validAlias {
		return ErrCodexPassiveRootAliasInvalid
	}
	bindingContext, cancelBinding := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	binding, found, err := LoadCodexRootChannelBindingForRoutingSideContext(bindingContext, userID, alias.RootID, alias.UARoutingOnly)
	cancelBinding()
	if err != nil {
		return err
	}
	if !found || binding.SelectedGroup != alias.SelectedGroup || CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	winner, selectedWon, err := ClaimProvisionalCodexRootChannelBinding(userID, alias.RootID, binding)
	if err != nil {
		return err
	}
	if !selectedWon || CodexRootChannelBindingFingerprint(winner) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	member := codexRecentRootCandidateMember(alias.RootID, alias.BindingFingerprint)
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(alias)
		if err != nil {
			return err
		}
		claimContext, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		baseScope := codexPassiveRootScopeKey(userID, tokenID, scope)
		normalScope := codexRecentRootChannelScopeKey(userID, tokenID, false, scope)
		uaScope := codexRecentRootChannelScopeKey(userID, tokenID, true, scope)
		stored, err := claimCodexTitleRootAliasScript.Run(claimContext, common.RDB, []string{
			codexPassiveRootAliasRedisKey(baseScope, cacheKey),
			codexTitleRootCandidateRedisKey(normalScope), codexTitleRootCandidateRedisKey(uaScope),
			codexRecentRootChannelRedisKey(normalScope), codexRecentRootChannelRedisKey(uaScope),
		}, string(payload), member, int64(codexPassiveRootAliasProvisionalTTL/time.Second)).Int()
		if err != nil {
			return err
		}
		if stored < 0 {
			return ErrCodexPassiveRootAliasConflict
		}
		if stored == 0 {
			return ErrCodexPassiveRootCandidatesChanged
		}
		return nil
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	aliasCache := getCodexPassiveRootAliasMemory()
	current, aliasFound, err := aliasCache.Get(cacheKey)
	if err != nil {
		return err
	}
	if aliasFound {
		if current != alias {
			return ErrCodexPassiveRootAliasConflict
		}
		return nil
	}
	nowMillis := time.Now().UTC().UnixMilli()
	activeMember := ""
	activeCount := 0
	for _, side := range []bool{false, true} {
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, side, scope)
		fresh, freshFound, freshErr := getCodexTitleRootMemory().Get(scopeKey)
		if freshErr != nil {
			return freshErr
		}
		recent, recentFound, recentErr := getCodexRecentRootMemory().Get(scopeKey)
		if recentErr != nil {
			return recentErr
		}
		if !freshFound || !recentFound {
			continue
		}
		for candidate, freshExpiresAt := range fresh {
			if freshExpiresAt > nowMillis && recent[candidate] > nowMillis {
				activeMember = candidate
				activeCount++
			}
		}
	}
	if activeCount != 1 || activeMember != member {
		return ErrCodexPassiveRootCandidatesChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	aliasCache.SetWithTTL(cacheKey, alias, codexPassiveRootAliasProvisionalTTL)
	return nil
}

// ClaimCodexPassiveRootAlias preserves the legacy single-candidate claim used
// by callers that do not carry an arrival-event proof.
func ClaimCodexPassiveRootAlias(ctx context.Context, userID, tokenID int, systemRootID string, alias CodexPassiveRootAlias, scopes ...CodexPassiveRootScope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope := normalizeCodexPassiveRootScope(userID, tokenID, scopes)
	cacheKey := codexPassiveRootAliasCacheKeyForScope(scope, systemRootID)
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	if cacheKey == "" || !validAlias {
		return ErrCodexPassiveRootAliasInvalid
	}
	currentAlias, aliasFound, err := LoadCodexPassiveRootAlias(ctx, userID, tokenID, systemRootID, scope)
	if err != nil {
		return err
	}
	if aliasFound {
		if currentAlias != alias {
			return ErrCodexPassiveRootAliasConflict
		}
	}
	bindingContext, cancelBinding := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	binding, bindingFound, bindingErr := LoadCodexRootChannelBindingForRoutingSideContext(bindingContext, userID, alias.RootID, alias.UARoutingOnly)
	cancelBinding()
	if bindingErr != nil {
		return bindingErr
	}
	if !bindingFound || binding.SelectedGroup != alias.SelectedGroup || binding.UARoutingOnly != alias.UARoutingOnly ||
		CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	// Keep the exact route alive beyond the new two-minute alias without
	// shortening an already durable binding. Re-claiming also closes the race
	// where the exact route expires/rebinds after the validation read above.
	winner, selectedWon, claimErr := ClaimProvisionalCodexRootChannelBinding(userID, alias.RootID, binding)
	if claimErr != nil {
		return claimErr
	}
	if !selectedWon || CodexRootChannelBindingFingerprint(winner) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	if aliasFound {
		return nil
	}
	candidateMember := codexRecentRootCandidateMember(alias.RootID, alias.BindingFingerprint)
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(alias)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, alias.UARoutingOnly, scope)
		redisScopeKey := codexPassiveRootScopeKey(userID, tokenID, scope)
		stored, err := claimCodexPassiveRootAliasScript.Run(ctx, common.RDB,
			[]string{codexPassiveRootAliasRedisKey(redisScopeKey, cacheKey), codexRecentRootChannelRedisKey(scopeKey)},
			string(payload), candidateMember, int64(codexPassiveRootAliasProvisionalTTL/time.Second)).Int()
		if err != nil {
			return err
		}
		if stored < 0 {
			return ErrCodexPassiveRootAliasConflict
		}
		if stored == 0 {
			return ErrCodexPassiveRootCandidatesChanged
		}
		return nil
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	cache := getCodexPassiveRootAliasMemory()
	current, found, err := cache.Get(cacheKey)
	if err != nil {
		return err
	}
	if found && current != alias {
		return ErrCodexPassiveRootAliasConflict
	}
	if found {
		return nil
	}
	if !found {
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, alias.UARoutingOnly, scope)
		candidateTimes, candidatesFound, candidateErr := getCodexRecentRootMemory().Get(scopeKey)
		if candidateErr != nil {
			return candidateErr
		}
		nowMillis := time.Now().UTC().UnixMilli()
		activeCandidate := ""
		activeCount := 0
		if candidatesFound {
			for member, expiresAt := range candidateTimes {
				if expiresAt > nowMillis {
					activeCandidate = member
					activeCount++
				}
			}
		}
		if activeCount != 1 || activeCandidate != candidateMember {
			return ErrCodexPassiveRootCandidatesChanged
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cache.SetWithTTL(cacheKey, alias, codexPassiveRootAliasProvisionalTTL)
	return nil
}

// ClaimCodexObservedPassiveRootAlias atomically freezes the exact predecessor
// event selected for an otherwise-unlinked system/summary request. A request
// that arrived after cutoff is excluded by its order, even if it publishes
// before this claim executes.
func ClaimCodexObservedPassiveRootAlias(
	ctx context.Context,
	userID, tokenID int,
	systemRootID string,
	alias CodexPassiveRootAlias,
	candidate CodexRecentRootChannelCandidate,
	cutoff CodexRequestArrival,
	scopes ...CodexPassiveRootScope,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope := normalizeCodexPassiveRootScope(userID, tokenID, scopes)
	cacheKey := codexPassiveRootAliasCacheKeyForScope(scope, systemRootID)
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	observationWindow := candidate.ObservationWindow
	if observationWindow <= 0 {
		observationWindow = codexRootObservationWindow
	}
	eventArrival, eventRootID, eventFingerprint, validEvent := parseCodexRootObservationMember(candidate.ObservationID)
	if cacheKey == "" || !validAlias || !cutoff.validFor(userID, tokenID, scope) || !validEvent ||
		eventRootID != alias.RootID || eventFingerprint != alias.BindingFingerprint ||
		eventArrival.Order != candidate.ArrivalOrder || !eventArrival.ArrivedAt.Equal(candidate.ArrivedAt) ||
		eventArrival.Order >= cutoff.Order || eventArrival.ArrivedAt.After(cutoff.ArrivedAt) ||
		eventArrival.ArrivedAt.Before(cutoff.ArrivedAt.Add(-observationWindow)) {
		return ErrCodexPassiveRootAliasInvalid
	}
	currentAlias, aliasFound, err := LoadCodexPassiveRootAlias(ctx, userID, tokenID, systemRootID, scope)
	if err != nil {
		return err
	}
	if aliasFound {
		if currentAlias != alias {
			return ErrCodexPassiveRootAliasConflict
		}
		return nil
	}
	bindingContext, cancelBinding := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	binding, bindingFound, bindingErr := LoadCodexRootChannelBindingForRoutingSideContext(bindingContext, userID, alias.RootID, alias.UARoutingOnly)
	cancelBinding()
	if bindingErr != nil {
		return bindingErr
	}
	if !bindingFound || binding.SelectedGroup != alias.SelectedGroup || binding.UARoutingOnly != alias.UARoutingOnly ||
		CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	winner, selectedWon, claimErr := ClaimProvisionalCodexRootChannelBinding(userID, alias.RootID, binding)
	if claimErr != nil {
		return claimErr
	}
	if !selectedWon || winner.SelectedGroup != alias.SelectedGroup || winner.UARoutingOnly != alias.UARoutingOnly ||
		CodexRootChannelBindingFingerprint(winner) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	if common.RedisEnabled && common.RDB != nil {
		payload, marshalErr := common.Marshal(alias)
		if marshalErr != nil {
			return marshalErr
		}
		claimContext, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, alias.UARoutingOnly, scope)
		redisScopeKey := codexPassiveRootScopeKey(userID, tokenID, scope)
		stored, claimErr := claimCodexObservedPassiveRootAliasScript.Run(claimContext, common.RDB,
			[]string{codexPassiveRootAliasRedisKey(redisScopeKey, cacheKey), codexRootObservationRedisKey(scopeKey)},
			string(payload), candidate.ObservationID, int64(codexPassiveRootAliasProvisionalTTL/time.Second), cutoff.Order,
			candidate.ArrivalOrder, cutoff.ArrivedAt.UnixMilli(), observationWindow/time.Millisecond).Int()
		if claimErr != nil {
			return claimErr
		}
		if stored < 0 {
			return ErrCodexPassiveRootAliasConflict
		}
		if stored == 0 {
			return ErrCodexPassiveRootCandidatesChanged
		}
		return nil
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	aliasCache := getCodexPassiveRootAliasMemory()
	current, found, cacheErr := aliasCache.Get(cacheKey)
	if cacheErr != nil {
		return cacheErr
	}
	if found {
		if current != alias {
			return ErrCodexPassiveRootAliasConflict
		}
		return nil
	}
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, alias.UARoutingOnly, scope)
	events, eventsFound, eventsErr := getCodexRootObservationMemory().Get(scopeKey)
	if eventsErr != nil {
		return eventsErr
	}
	latestEvent := ""
	latestOrder := int64(0)
	if eventsFound {
		for eventID, order := range events {
			if order >= cutoff.Order {
				continue
			}
			eventArrival, _, _, valid := parseCodexRootObservationMember(eventID)
			if !valid || eventArrival.ArrivedAt.Before(cutoff.ArrivedAt.Add(-observationWindow)) {
				continue
			}
			if latestEvent == "" || order > latestOrder || (order == latestOrder && eventID < latestEvent) {
				latestEvent = eventID
				latestOrder = order
			}
		}
	}
	if latestEvent != candidate.ObservationID || latestOrder != candidate.ArrivalOrder {
		return ErrCodexPassiveRootCandidatesChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	aliasCache.SetWithTTL(cacheKey, alias, codexPassiveRootAliasProvisionalTTL)
	return nil
}

// PromoteCodexPassiveRootAlias makes a successfully dispatched passive route
// durable together with its exact root channel binding. Failed passive calls
// retain only the short provisional alias and cannot poison retries for 24h.
func PromoteCodexPassiveRootAlias(ctx context.Context, userID, tokenID int, systemRootID string, alias CodexPassiveRootAlias, scopes ...CodexPassiveRootScope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope := normalizeCodexPassiveRootScope(userID, tokenID, scopes)
	cacheKey := codexPassiveRootAliasCacheKeyForScope(scope, systemRootID)
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	if cacheKey == "" || !validAlias {
		return ErrCodexPassiveRootAliasInvalid
	}
	currentAlias, aliasFound, err := LoadCodexPassiveRootAlias(ctx, userID, tokenID, systemRootID, scope)
	if err != nil {
		return err
	}
	if !aliasFound || currentAlias != alias {
		return ErrCodexPassiveRootAliasConflict
	}
	bindingContext, cancelBinding := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	binding, found, err := LoadCodexRootChannelBindingForRoutingSideContext(bindingContext, userID, alias.RootID, alias.UARoutingOnly)
	cancelBinding()
	if err != nil {
		return err
	}
	if !found || binding.SelectedGroup != alias.SelectedGroup || binding.UARoutingOnly != alias.UARoutingOnly ||
		CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
		return errors.New("Codex passive root binding is unavailable for promotion")
	}
	if err := StoreCodexRootChannelBinding(userID, alias.RootID, binding); err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.Marshal(alias)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		promoted, err := promoteCodexPassiveRootAliasScript.Run(ctx, common.RDB,
			[]string{codexPassiveRootAliasRedisKey(codexPassiveRootScopeKey(userID, tokenID, scope), cacheKey)},
			string(payload), int64(codexPassiveRootAliasTTL/time.Second)).Int()
		if err != nil {
			return err
		}
		if promoted != 1 {
			return ErrCodexPassiveRootAliasConflict
		}
		return nil
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	cache := getCodexPassiveRootAliasMemory()
	current, aliasFound, err := cache.Get(cacheKey)
	if err != nil {
		return err
	}
	if !aliasFound || current != alias {
		return ErrCodexPassiveRootAliasConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cache.SetWithTTL(cacheKey, alias, codexPassiveRootAliasTTL)
	return nil
}

func LoadCodexPassiveRootAlias(ctx context.Context, userID, tokenID int, systemRootID string, scopes ...CodexPassiveRootScope) (CodexPassiveRootAlias, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CodexPassiveRootAlias{}, false, err
	}
	scope := normalizeCodexPassiveRootScope(userID, tokenID, scopes)
	cacheKey := codexPassiveRootAliasCacheKeyForScope(scope, systemRootID)
	if cacheKey == "" {
		return CodexPassiveRootAlias{}, false, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		payload, err := common.RDB.Get(ctx, codexPassiveRootAliasRedisKey(codexPassiveRootScopeKey(userID, tokenID, scope), cacheKey)).Bytes()
		if errors.Is(err, redis.Nil) {
			return CodexPassiveRootAlias{}, false, nil
		}
		if err != nil {
			return CodexPassiveRootAlias{}, false, err
		}
		alias := CodexPassiveRootAlias{}
		if err := common.Unmarshal(payload, &alias); err != nil {
			return CodexPassiveRootAlias{}, false, err
		}
		alias, validAlias := normalizeCodexPassiveRootAlias(alias)
		if !validAlias {
			return CodexPassiveRootAlias{}, false, ErrCodexPassiveRootAliasInvalid
		}
		return alias, true, nil
	}
	alias, found, err := getCodexPassiveRootAliasMemory().Get(cacheKey)
	if err != nil || !found {
		return alias, found, err
	}
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	if !validAlias {
		return CodexPassiveRootAlias{}, false, ErrCodexPassiveRootAliasInvalid
	}
	return alias, true, nil
}

func normalizeCodexPassiveRootAlias(alias CodexPassiveRootAlias) (CodexPassiveRootAlias, bool) {
	alias.RootID = strings.TrimSpace(alias.RootID)
	alias.SelectedGroup = strings.TrimSpace(alias.SelectedGroup)
	alias.BindingFingerprint = strings.TrimSpace(alias.BindingFingerprint)
	return alias, alias.RootID != "" && alias.SelectedGroup != "" && alias.BindingFingerprint != ""
}
