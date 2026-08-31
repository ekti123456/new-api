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
	codexRecentRootChannelCandidateNamespace = "new-api:codex_recent_root_channel:v2"
	codexPassiveRootAliasNamespace           = "new-api:codex_passive_root_alias:v1"
	codexRecentRootChannelCandidateTTL       = 10 * time.Minute
	codexProvisionalRootCandidateTTL         = 2 * time.Minute
	codexPassiveRootAliasProvisionalTTL      = 2 * time.Minute
	codexPassiveRootAliasTTL                 = 24 * time.Hour
	codexRecentRootChannelCandidateLimit     = 32
	codexPassiveRootRedisTimeout             = 500 * time.Millisecond
	codexRecentRootPollInterval              = 200 * time.Millisecond
)

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
}

type CodexPassiveRootAlias struct {
	RootID             string `json:"root_id"`
	SelectedGroup      string `json:"selected_group"`
	UARoutingOnly      bool   `json:"ua_routing_only"`
	BindingFingerprint string `json:"binding_fingerprint"`
}

type codexRecentRootWaiter struct {
	updates chan struct{}
	count   int
}

var (
	codexRecentRootMemoryOnce sync.Once
	codexRecentRootMemory     *hot.HotCache[string, map[string]int64]
	codexRecentRootMemoryMu   sync.Mutex

	codexPassiveRootAliasMemoryOnce sync.Once
	codexPassiveRootAliasMemory     *hot.HotCache[string, CodexPassiveRootAlias]

	codexRecentRootWaiters = struct {
		sync.Mutex
		items map[string]*codexRecentRootWaiter
	}{items: make(map[string]*codexRecentRootWaiter)}
)

var storeCodexRecentRootCandidateScript = redis.NewScript(`
local redis_time = redis.call('TIME')
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
local expires_at = now_ms + tonumber(ARGV[1])
local current_expires_at = redis.call('ZSCORE', KEYS[1], ARGV[2])
if not current_expires_at or tonumber(current_expires_at) < expires_at then
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

var promoteCodexPassiveRootAliasScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current or current ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
return 1
`)

func getCodexRecentRootMemory() *hot.HotCache[string, map[string]int64] {
	codexRecentRootMemoryOnce.Do(func() {
		codexRecentRootMemory = hot.NewHotCache[string, map[string]int64](hot.LRU, 100_000).
			WithTTL(codexRecentRootChannelCandidateTTL).
			WithJanitor().
			Build()
	})
	return codexRecentRootMemory
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

func codexRecentRootChannelScopeKey(userID, tokenID int, uaRoutingOnly bool) string {
	baseScope := codexPassiveRootRedisScopeKey(userID, tokenID)
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

func codexPassiveRootRedisScopeKey(userID, tokenID int) string {
	if userID <= 0 || tokenID <= 0 {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + strconv.Itoa(tokenID)))
	return hex.EncodeToString(digest[:])
}

func codexPassiveRootAliasCacheKey(userID, tokenID int, systemRootID string) string {
	systemRootID = strings.TrimSpace(systemRootID)
	if userID <= 0 || tokenID <= 0 || systemRootID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + strconv.Itoa(tokenID) + "\x00" + systemRootID))
	return hex.EncodeToString(digest[:])
}

func codexPassiveRootAliasRedisKey(scopeKey, cacheKey string) string {
	if scopeKey == "" || cacheKey == "" {
		return ""
	}
	return cachex.Namespace(codexPassiveRootAliasNamespace).FullKey("{" + scopeKey + "}:" + cacheKey)
}

func StoreRecentCodexRootChannelBinding(userID, tokenID int, rootID string, binding CodexRootChannelBinding) error {
	rootID = strings.TrimSpace(rootID)
	if codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly) == "" ||
		codexRecentRootCandidateMember(rootID, CodexRootChannelBindingFingerprint(binding)) == "" {
		return nil
	}
	if err := StoreCodexRootChannelBinding(userID, rootID, binding); err != nil {
		return err
	}
	return StoreRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding)
}

func StoreProvisionalRecentCodexRootChannelBinding(userID, tokenID int, rootID string, binding CodexRootChannelBinding) error {
	rootID = strings.TrimSpace(rootID)
	if codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly) == "" ||
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
	return StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, winner)
}

// StoreRecentCodexRootChannelCandidate records an already-persisted successful
// root binding as a durable passive-route candidate.
func StoreRecentCodexRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding) error {
	return storeValidatedRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, codexRecentRootChannelCandidateTTL)
}

// StoreProvisionalRecentCodexRootChannelCandidate records an already-claimed
// in-flight root binding without extending either provisional lifetime.
func StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding) error {
	return storeValidatedRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, codexProvisionalRootCandidateTTL)
}

func storeValidatedRecentCodexRootChannelCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding, activeTTL time.Duration) error {
	rootID = strings.TrimSpace(rootID)
	bindingFingerprint := CodexRootChannelBindingFingerprint(binding)
	candidateMember := codexRecentRootCandidateMember(rootID, bindingFingerprint)
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, binding.UARoutingOnly)
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
		ctx, cancel := context.WithTimeout(context.Background(), codexPassiveRootRedisTimeout)
		defer cancel()
		_, err = storeCodexRecentRootCandidateScript.Run(ctx, common.RDB, []string{codexRecentRootChannelRedisKey(scopeKey)},
			int64(activeTTL/time.Millisecond), candidateMember,
			codexRecentRootChannelCandidateLimit, int64(codexRecentRootChannelCandidateTTL/time.Second)).Result()
		if err != nil {
			return err
		}
	} else {
		storeCodexRecentRootCandidateMemory(scopeKey, candidateMember, now.Add(activeTTL))
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

func storeCodexRecentRootCandidateMemory(scopeKey, candidateMember string, expiresAt time.Time) {
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
	if currentExpiresAt, exists := current[candidateMember]; !exists || currentExpiresAt < newExpiresAt {
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
	cache.SetWithTTL(scopeKey, current, codexRecentRootChannelCandidateTTL)
}

func LoadRecentCodexRootChannelCandidates(ctx context.Context, userID, tokenID int, uaRoutingOnly bool) ([]CodexRecentRootChannelCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, uaRoutingOnly)
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
		cache.SetWithTTL(scopeKey, updated, codexRecentRootChannelCandidateTTL)
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
		cache.SetWithTTL(scopeKey, active, codexRecentRootChannelCandidateTTL)
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

func ClaimCodexPassiveRootAlias(ctx context.Context, userID, tokenID int, systemRootID string, alias CodexPassiveRootAlias) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cacheKey := codexPassiveRootAliasCacheKey(userID, tokenID, systemRootID)
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	if cacheKey == "" || !validAlias {
		return ErrCodexPassiveRootAliasInvalid
	}
	currentAlias, aliasFound, err := LoadCodexPassiveRootAlias(ctx, userID, tokenID, systemRootID)
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
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, alias.UARoutingOnly)
		redisScopeKey := codexPassiveRootRedisScopeKey(userID, tokenID)
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
		scopeKey := codexRecentRootChannelScopeKey(userID, tokenID, alias.UARoutingOnly)
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

// PromoteCodexPassiveRootAlias makes a successfully dispatched passive route
// durable together with its exact root channel binding. Failed passive calls
// retain only the short provisional alias and cannot poison retries for 24h.
func PromoteCodexPassiveRootAlias(ctx context.Context, userID, tokenID int, systemRootID string, alias CodexPassiveRootAlias) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cacheKey := codexPassiveRootAliasCacheKey(userID, tokenID, systemRootID)
	alias, validAlias := normalizeCodexPassiveRootAlias(alias)
	if cacheKey == "" || !validAlias {
		return ErrCodexPassiveRootAliasInvalid
	}
	currentAlias, aliasFound, err := LoadCodexPassiveRootAlias(ctx, userID, tokenID, systemRootID)
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
			[]string{codexPassiveRootAliasRedisKey(codexPassiveRootRedisScopeKey(userID, tokenID), cacheKey)},
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

func LoadCodexPassiveRootAlias(ctx context.Context, userID, tokenID int, systemRootID string) (CodexPassiveRootAlias, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CodexPassiveRootAlias{}, false, err
	}
	cacheKey := codexPassiveRootAliasCacheKey(userID, tokenID, systemRootID)
	if cacheKey == "" {
		return CodexPassiveRootAlias{}, false, nil
	}
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
		defer cancel()
		payload, err := common.RDB.Get(ctx, codexPassiveRootAliasRedisKey(codexPassiveRootRedisScopeKey(userID, tokenID), cacheKey)).Bytes()
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
