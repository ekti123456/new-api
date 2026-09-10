package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
)

type codexInitialTitleRootState struct {
	RootID             string                `json:"root_id"`
	Scope              CodexPassiveRootScope `json:"scope"`
	BindingFingerprint string                `json:"binding_fingerprint"`
	UARoutingOnly      bool                  `json:"ua_routing_only"`
	FirstSeenMillis    int64                 `json:"first_seen_millis"`
	Owner              string                `json:"owner"`
}

var codexInitialTitleRootMemory = hot.NewHotCache[string, map[string]int64](hot.LRU, 100_000).
	WithTTL(codexTitleRootCandidateTTL).WithJanitor().Build()

var codexInitialTitleStateMemory = hot.NewHotCache[string, codexInitialTitleRootState](hot.LRU, 100_000).
	WithTTL(codexRootChannelCacheTTL).WithJanitor().Build()

var codexInitialTitleStateMu sync.Mutex

var compareCodexInitialTitleStateScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if ARGV[1] == '' then
  if current then return 0 end
elseif current ~= ARGV[1] then
  return 0
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then ttl = tonumber(ARGV[3]) end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ttl)
return 1
`)

func codexInitialTitleRootCandidateRedisKey(scopeKey string) string {
	baseScope, _, _ := strings.Cut(scopeKey, ":")
	return cachex.Namespace("new-api:codex_initial_title_candidate:v1").FullKey("{" + baseScope + "}:" + scopeKey)
}

func codexInitialTitleStateKey(userID int, rootID string) string {
	return cachex.Namespace("new-api:codex_initial_title_state:v1").FullKey(legacyCodexRootChannelCacheKey(userID, rootID))
}

func loadCodexInitialTitleState(ctx context.Context, userID int, rootID string) (codexInitialTitleRootState, bool, error) {
	if err := ctx.Err(); err != nil {
		return codexInitialTitleRootState{}, false, err
	}
	key := codexInitialTitleStateKey(userID, rootID)
	if common.RedisEnabled && common.RDB != nil {
		payload, err := common.RDB.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return codexInitialTitleRootState{}, false, nil
		}
		if err != nil {
			return codexInitialTitleRootState{}, false, err
		}
		var state codexInitialTitleRootState
		err = common.Unmarshal(payload, &state)
		return state, err == nil, err
	}
	return codexInitialTitleStateMemory.Get(key)
}

func compareCodexInitialTitleState(ctx context.Context, userID int, rootID string, previous *codexInitialTitleRootState, next codexInitialTitleRootState) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key := codexInitialTitleStateKey(userID, rootID)
	if common.RedisEnabled && common.RDB != nil {
		var expected []byte
		var err error
		if previous != nil {
			expected, err = common.Marshal(previous)
			if err != nil {
				return false, err
			}
		}
		payload, err := common.Marshal(next)
		if err != nil {
			return false, err
		}
		changed, err := compareCodexInitialTitleStateScript.Run(ctx, common.RDB, []string{key}, string(expected), string(payload), codexRootChannelCacheTTL.Milliseconds()).Int()
		return changed == 1, err
	}
	codexInitialTitleStateMu.Lock()
	defer codexInitialTitleStateMu.Unlock()
	current, found, err := codexInitialTitleStateMemory.Get(key)
	if err != nil {
		return false, err
	}
	if previous == nil && found || previous != nil && (!found || current != *previous) {
		return false, nil
	}
	codexInitialTitleStateMemory.Set(key, next)
	return true, nil
}

func StoreInitialCodexTitleRootCandidate(userID, tokenID int, rootID string, binding CodexRootChannelBinding, scope CodexPassiveRootScope) error {
	rootID = strings.TrimSpace(rootID)
	fingerprint := CodexRootChannelBindingFingerprint(binding)
	if userID <= 0 || tokenID <= 0 || rootID == "" || fingerprint == "" {
		return ErrCodexPassiveRootAliasInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexPassiveRootRedisTimeout)
	defer cancel()
	state := codexInitialTitleRootState{
		RootID: rootID, Scope: normalizeCodexPassiveRootScope(userID, tokenID, []CodexPassiveRootScope{scope}),
		BindingFingerprint: fingerprint, UARoutingOnly: binding.UARoutingOnly, FirstSeenMillis: time.Now().UTC().UnixMilli(),
	}
	created, err := compareCodexInitialTitleState(ctx, userID, rootID, nil, state)
	if err != nil || !created {
		return err
	}
	if err := storeCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding, true, state.Scope); err != nil {
		return err
	}
	current, found, err := loadCodexInitialTitleState(ctx, userID, rootID)
	if err != nil {
		return err
	}
	if !found || current != state {
		return removeCodexInitialTitleRootCandidate(ctx, state)
	}
	return nil
}

func removeCodexInitialTitleRootCandidate(ctx context.Context, state codexInitialTitleRootState) error {
	if state.BindingFingerprint == "" || state.Scope.UserID <= 0 {
		return nil
	}
	scopeKey := codexRecentRootChannelScopeKey(state.Scope.UserID, state.Scope.TokenID, state.UARoutingOnly, state.Scope)
	member := codexRecentRootCandidateMember(state.RootID, state.BindingFingerprint)
	if common.RedisEnabled && common.RDB != nil {
		return common.RDB.ZRem(ctx, codexInitialTitleRootCandidateRedisKey(scopeKey), member).Err()
	}
	codexRecentRootMemoryMu.Lock()
	defer codexRecentRootMemoryMu.Unlock()
	current, found, err := codexInitialTitleRootMemory.Get(scopeKey)
	if err != nil || !found {
		return err
	}
	updated := make(map[string]int64, len(current))
	for candidate, expiresAt := range current {
		if candidate != member {
			updated[candidate] = expiresAt
		}
	}
	codexInitialTitleRootMemory.Set(scopeKey, updated)
	return nil
}

func ClaimCodexInitialTitleRootAlias(ctx context.Context, userID, tokenID int, sourceRootID string, alias CodexPassiveRootAlias, scope CodexPassiveRootScope) error {
	if userID <= 0 || tokenID <= 0 || strings.TrimSpace(sourceRootID) == "" || strings.TrimSpace(alias.RootID) == "" {
		return ErrCodexPassiveRootAliasInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	defer cancel()
	state, found, err := loadCodexInitialTitleState(ctx, userID, alias.RootID)
	if err != nil {
		return err
	}
	if !found || state.Scope != normalizeCodexPassiveRootScope(userID, tokenID, []CodexPassiveRootScope{scope}) ||
		state.BindingFingerprint != alias.BindingFingerprint || state.UARoutingOnly != alias.UARoutingOnly || state.Owner != "" && state.Owner != sourceRootID {
		return ErrCodexPassiveRootCandidatesChanged
	}
	claimed := state
	claimed.Owner = sourceRootID
	changed, err := compareCodexInitialTitleState(ctx, userID, alias.RootID, &state, claimed)
	if err != nil {
		return err
	}
	if !changed {
		return ErrCodexPassiveRootCandidatesChanged
	}
	if err := claimCodexUniquePassiveRootAlias(ctx, userID, tokenID, sourceRootID, alias, true, true, scope); err != nil {
		rollbackContext, rollbackCancel := context.WithTimeout(context.WithoutCancel(ctx), codexPassiveRootRedisTimeout)
		defer rollbackCancel()
		_, rollbackErr := compareCodexInitialTitleState(rollbackContext, userID, alias.RootID, &claimed, state)
		return errors.Join(err, rollbackErr)
	}
	return removeCodexInitialTitleRootCandidate(ctx, claimed)
}

func MarkCodexInitialTitleRootNamed(ctx context.Context, userID int, rootID string) error {
	if userID <= 0 || strings.TrimSpace(rootID) == "" {
		return ErrCodexPassiveRootAliasInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, codexPassiveRootRedisTimeout)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		state, found, err := loadCodexInitialTitleState(ctx, userID, rootID)
		if err != nil {
			return err
		}
		if found && state.Owner != "" {
			return removeCodexInitialTitleRootCandidate(ctx, state)
		}
		var previous *codexInitialTitleRootState
		if found {
			previous = &state
		}
		named := state
		named.RootID = rootID
		named.Owner = "named"
		changed, err := compareCodexInitialTitleState(ctx, userID, rootID, previous, named)
		if err != nil {
			return err
		}
		if changed {
			return removeCodexInitialTitleRootCandidate(ctx, named)
		}
	}
	return ErrCodexPassiveRootCandidatesChanged
}
