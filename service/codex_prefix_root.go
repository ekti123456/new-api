package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/samber/hot"
)

const CodexPrefixRootAssociation = "scope_prefix_unique"
const codexPrefixRootLimit = 64
const codexPrefixRootOverflow = "!overflow"

var ErrCodexPrefixRootAmbiguous = errors.New("同一身份范围及会话前缀对应多个主会话")
var ErrCodexSessionPrefixUnavailable = errors.New("缺少有效且一致的原始 session_id，无法匹配会话前缀")
var codexPrefixRootMu sync.Mutex
var codexPrefixRootMemory = hot.NewHotCache[string, map[string]string](hot.LRU, 100_000).
	WithTTL(codexRootChannelCacheTTL).WithJanitor().Build()

var storeCodexPrefixRootScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[4]) == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[5])
  return 0
end
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 0 and redis.call('HLEN', KEYS[1]) >= tonumber(ARGV[3]) then
  redis.call('DEL', KEYS[1])
  redis.call('HSET', KEYS[1], ARGV[4], '')
  redis.call('PEXPIRE', KEYS[1], ARGV[5])
  return 0
end
local existing = redis.call('HGET', KEYS[1], ARGV[1])
if existing and existing ~= ARGV[2] then return -1 end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
return 1
`)

var claimCodexPrefixRootScript = redis.NewScript(`
local existing = redis.call('GET', KEYS[2])
if existing then
  if existing == ARGV[3] then return 1 end
  return -1
end
if redis.call('HLEN', KEYS[1]) ~= 1 or redis.call('HGET', KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
return 1
`)

func CodexSessionIDPrefix(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) != 36 {
		return ""
	}
	parsed, err := uuid.Parse(sessionID)
	if err != nil || parsed == uuid.Nil || !strings.EqualFold(parsed.String(), sessionID) {
		return ""
	}
	return strings.ToLower(sessionID[:8])
}

func codexPrefixRootKey(scope CodexPassiveRootScope, sessionID string) string {
	scopeKey := codexPassiveRootScopeKey(scope.UserID, scope.TokenID, scope)
	prefix := CodexSessionIDPrefix(sessionID)
	if scopeKey == "" || prefix == "" {
		return ""
	}
	return cachex.Namespace("new-api:codex_prefix_root:v1").FullKey("{" + scopeKey + "}:" + prefix)
}

func StoreCodexPrefixRootCandidate(ctx context.Context, scope CodexPassiveRootScope, sessionID, rootID string, binding CodexRootChannelBinding) error {
	key := codexPrefixRootKey(scope, sessionID)
	rootID = strings.TrimSpace(rootID)
	fingerprint := CodexRootChannelBindingFingerprint(binding)
	if key == "" || rootID == "" || fingerprint == "" {
		return ErrCodexSessionPrefixUnavailable
	}
	alias := CodexPassiveRootAlias{RootID: rootID, SelectedGroup: binding.SelectedGroup,
		UARoutingOnly: binding.UARoutingOnly, BindingFingerprint: fingerprint, Association: CodexPrefixRootAssociation}
	payload, err := common.Marshal(alias)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	defer cancel()
	if common.RedisEnabled && common.RDB != nil {
		stored, err := storeCodexPrefixRootScript.Run(ctx, common.RDB, []string{key}, rootID, string(payload),
			codexPrefixRootLimit, codexPrefixRootOverflow, codexRootChannelCacheTTL.Milliseconds()).Int()
		if err != nil {
			return err
		}
		if stored < 0 {
			return ErrCodexPassiveRootAliasConflict
		}
		if stored == 0 {
			return ErrCodexPrefixRootAmbiguous
		}
		return nil
	}
	codexPrefixRootMu.Lock()
	defer codexPrefixRootMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, _, err := codexPrefixRootMemory.Get(key)
	if err != nil {
		return err
	}
	if _, overflow := current[codexPrefixRootOverflow]; overflow {
		codexPrefixRootMemory.Set(key, map[string]string{codexPrefixRootOverflow: ""})
		return ErrCodexPrefixRootAmbiguous
	}
	if previous, exists := current[rootID]; exists && previous != string(payload) {
		return ErrCodexPassiveRootAliasConflict
	}
	if _, exists := current[rootID]; !exists && len(current) >= codexPrefixRootLimit {
		codexPrefixRootMemory.Set(key, map[string]string{codexPrefixRootOverflow: ""})
		return ErrCodexPrefixRootAmbiguous
	}
	updated := make(map[string]string, len(current)+1)
	for candidateID, candidate := range current {
		updated[candidateID] = candidate
	}
	updated[rootID] = string(payload)
	codexPrefixRootMemory.Set(key, updated)
	return nil
}

func LoadCodexPrefixRootCandidates(ctx context.Context, scope CodexPassiveRootScope, sessionID string) ([]CodexRecentRootChannelCandidate, error) {
	key := codexPrefixRootKey(scope, sessionID)
	if key == "" {
		return nil, ErrCodexSessionPrefixUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var entries map[string]string
	var err error
	if common.RedisEnabled && common.RDB != nil {
		entries, err = common.RDB.HGetAll(ctx, key).Result()
	} else {
		entries, _, err = codexPrefixRootMemory.Get(key)
	}
	if err != nil {
		return nil, err
	}
	if _, overflow := entries[codexPrefixRootOverflow]; overflow {
		return nil, ErrCodexPrefixRootAmbiguous
	}
	candidates := make([]CodexRecentRootChannelCandidate, 0, len(entries))
	for rootID, payload := range entries {
		candidate := CodexRecentRootChannelCandidate{RootID: rootID}
		if len(entries) == 1 {
			var alias CodexPassiveRootAlias
			if common.UnmarshalJsonStr(payload, &alias) != nil || alias.RootID != rootID || alias.Association != CodexPrefixRootAssociation {
				return nil, ErrCodexPassiveRootAliasInvalid
			}
			binding, found, err := LoadCodexRootChannelBindingForRoutingSideContext(ctx, scope.UserID, rootID, alias.UARoutingOnly)
			if err != nil {
				return nil, err
			}
			if !found || binding.SelectedGroup != alias.SelectedGroup || CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
				return nil, ErrCodexRecentRootBindingUnavailable
			}
			candidate.Binding, candidate.BindingFingerprint = binding, alias.BindingFingerprint
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].RootID < candidates[right].RootID })
	return candidates, nil
}

func ClaimCodexPrefixRootAlias(ctx context.Context, scope CodexPassiveRootScope, sessionID, sourceRootID string, alias CodexPassiveRootAlias, initialTitle bool) (claimErr error) {
	scope = normalizeCodexPassiveRootScope(scope.UserID, scope.TokenID, []CodexPassiveRootScope{scope})
	key := codexPrefixRootKey(scope, sessionID)
	cacheKey := codexPassiveRootAliasCacheKeyForScope(scope, sourceRootID)
	alias, valid := normalizeCodexPassiveRootAlias(alias)
	if key == "" || cacheKey == "" || !valid || alias.Association != CodexPrefixRootAssociation || alias.Temporary {
		return ErrCodexPassiveRootAliasInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	defer cancel()
	binding, found, err := LoadCodexRootChannelBindingForRoutingSideContext(ctx, scope.UserID, alias.RootID, alias.UARoutingOnly)
	if err != nil {
		return err
	}
	if !found || binding.SelectedGroup != alias.SelectedGroup || CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
		return ErrCodexRecentRootBindingUnavailable
	}
	winner, selectedWon, err := ClaimProvisionalCodexRootChannelBinding(scope.UserID, alias.RootID, binding)
	if err != nil {
		return err
	}
	if !selectedWon || CodexRootChannelBindingFingerprint(winner) != alias.BindingFingerprint {
		return ErrCodexPassiveRootCandidatesChanged
	}
	if initialTitle {
		state, found, err := loadCodexInitialTitleState(ctx, scope.UserID, alias.RootID)
		if err != nil {
			return err
		}
		if found && state.Owner != "" && state.Owner != sourceRootID {
			return ErrCodexPassiveRootCandidatesChanged
		}
		if found && state.Scope.UserID > 0 && (state.Scope != scope || state.BindingFingerprint != alias.BindingFingerprint) {
			return ErrCodexPassiveRootCandidatesChanged
		}
		var previous *codexInitialTitleRootState
		if found {
			previous = &state
		}
		claimed := codexInitialTitleRootState{RootID: alias.RootID, Scope: scope, BindingFingerprint: alias.BindingFingerprint,
			UARoutingOnly: alias.UARoutingOnly, FirstSeenMillis: time.Now().UTC().UnixMilli(), Owner: sourceRootID}
		changed, err := compareCodexInitialTitleState(ctx, scope.UserID, alias.RootID, previous, claimed)
		if err != nil {
			return err
		}
		if !changed {
			return ErrCodexPassiveRootCandidatesChanged
		}
		defer func() {
			if claimErr == nil {
				return
			}
			rollbackContext, rollbackCancel := context.WithTimeout(context.WithoutCancel(ctx), codexPassiveRootRedisTimeout)
			defer rollbackCancel()
			_, rollbackErr := compareCodexInitialTitleState(rollbackContext, scope.UserID, alias.RootID, &claimed, state)
			claimErr = errors.Join(claimErr, rollbackErr)
		}()
	}
	payload, err := common.Marshal(alias)
	if err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		aliasKey := codexPassiveRootAliasRedisKey(codexPassiveRootScopeKey(scope.UserID, scope.TokenID, scope), cacheKey)
		stored, err := claimCodexPrefixRootScript.Run(ctx, common.RDB, []string{key, aliasKey}, alias.RootID,
			string(payload), string(payload), codexPassiveRootAliasProvisionalTTL.Milliseconds()).Int()
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
	codexPrefixRootMu.Lock()
	defer codexPrefixRootMu.Unlock()
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, found, err := getCodexPassiveRootAliasMemory().Get(cacheKey)
	if err != nil {
		return err
	}
	if found {
		if current != alias {
			return ErrCodexPassiveRootAliasConflict
		}
		return nil
	}
	entries, _, err := codexPrefixRootMemory.Get(key)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[alias.RootID] != string(payload) {
		return ErrCodexPassiveRootCandidatesChanged
	}
	getCodexPassiveRootAliasMemory().SetWithTTL(cacheKey, alias, codexPassiveRootAliasProvisionalTTL)
	return nil
}
