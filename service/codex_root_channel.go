package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
)

const (
	codexRootChannelCacheNamespace       = "new-api:codex_root_channel:v1"
	codexRecentRootChannelCacheNamespace = "new-api:codex_recent_root_channel:v1"
	codexRecentUserGroupCacheNamespace   = "new-api:codex_recent_user_group_channel:v1"
	codexRootChannelCacheTTL             = 24 * time.Hour
	codexProvisionalRootChannelCacheTTL  = 2 * time.Minute
	codexRecentRootChannelCacheTTL       = 2 * time.Minute
)

// CodexRootChannelBinding pins both the NewAPI channel and its selected key.
// Only a key fingerprint is cached; bearer credentials are never stored here.
type CodexRootChannelBinding struct {
	ChannelID      int    `json:"channel_id"`
	SelectedGroup  string `json:"selected_group"`
	KeyIndex       int    `json:"key_index"`
	KeyFingerprint string `json:"key_fingerprint"`
	UARoutingOnly  bool   `json:"ua_routing_only"`
}

// CodexRecentRootChannelBinding is a short-lived, user/token-scoped bridge for
// Codex metadata generations (initial title and activity summary) that the
// desktop client starts as independent system threads without a parent ID.
// The short TTL intentionally limits temporal correlation to the root request
// that immediately preceded the metadata generation.
type CodexRecentRootChannelBinding struct {
	RootID  string                  `json:"root_id"`
	Binding CodexRootChannelBinding `json:"binding"`
}

var (
	codexRootChannelCacheOnce sync.Once
	codexRootChannelCache     *cachex.HybridCache[CodexRootChannelBinding]
	codexRecentRootCacheOnce  sync.Once
	codexRecentRootCache      *cachex.HybridCache[CodexRecentRootChannelBinding]
	codexRecentUserGroupOnce  sync.Once
	codexRecentUserGroupCache *cachex.HybridCache[CodexRecentRootChannelBinding]
)

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

func getCodexRecentRootChannelCache() *cachex.HybridCache[CodexRecentRootChannelBinding] {
	codexRecentRootCacheOnce.Do(func() {
		codexRecentRootCache = cachex.NewHybridCache[CodexRecentRootChannelBinding](cachex.HybridCacheConfig[CodexRecentRootChannelBinding]{
			Namespace: cachex.Namespace(codexRecentRootChannelCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[CodexRecentRootChannelBinding]{},
			Memory: func() *hot.HotCache[string, CodexRecentRootChannelBinding] {
				return hot.NewHotCache[string, CodexRecentRootChannelBinding](hot.LRU, 100_000).
					WithTTL(codexRecentRootChannelCacheTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return codexRecentRootCache
}

func getCodexRecentUserGroupChannelCache() *cachex.HybridCache[CodexRecentRootChannelBinding] {
	codexRecentUserGroupOnce.Do(func() {
		codexRecentUserGroupCache = cachex.NewHybridCache[CodexRecentRootChannelBinding](cachex.HybridCacheConfig[CodexRecentRootChannelBinding]{
			Namespace: cachex.Namespace(codexRecentUserGroupCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[CodexRecentRootChannelBinding]{},
			Memory: func() *hot.HotCache[string, CodexRecentRootChannelBinding] {
				return hot.NewHotCache[string, CodexRecentRootChannelBinding](hot.LRU, 100_000).
					WithTTL(codexRecentRootChannelCacheTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return codexRecentUserGroupCache
}

func codexRootChannelCacheKey(userID int, rootID string) string {
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
// it with the durable 24-hour binding; a failed request leaves at most a short
// two-minute stale entry.
func StoreProvisionalCodexRootChannelBinding(userID int, rootID string, binding CodexRootChannelBinding) error {
	if _, found, err := LoadCodexRootChannelBinding(userID, rootID); err != nil {
		return err
	} else if found {
		// Never shorten a durable binding when a later turn for the same root
		// starts. Existing roots are routed through this stored binding first.
		return nil
	}
	return storeCodexRootChannelBindingWithTTL(userID, rootID, binding, codexProvisionalRootChannelCacheTTL)
}

func storeCodexRootChannelBindingWithTTL(userID int, rootID string, binding CodexRootChannelBinding, ttl time.Duration) error {
	key := codexRootChannelCacheKey(userID, rootID)
	if key == "" || binding.ChannelID <= 0 || strings.TrimSpace(binding.SelectedGroup) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return nil
	}
	return getCodexRootChannelCache().SetWithTTL(key, binding, ttl)
}

func LoadCodexRootChannelBinding(userID int, rootID string) (CodexRootChannelBinding, bool, error) {
	key := codexRootChannelCacheKey(userID, rootID)
	if key == "" {
		return CodexRootChannelBinding{}, false, nil
	}
	return getCodexRootChannelCache().Get(key)
}

func codexRecentRootChannelCacheKey(userID, tokenID int) string {
	if userID <= 0 || tokenID <= 0 {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + strconv.Itoa(tokenID)))
	return hex.EncodeToString(digest[:])
}

func StoreRecentCodexRootChannelBinding(userID, tokenID int, rootID string, binding CodexRootChannelBinding) error {
	key := codexRecentRootChannelCacheKey(userID, tokenID)
	rootID = strings.TrimSpace(rootID)
	if key == "" || rootID == "" || binding.ChannelID <= 0 || strings.TrimSpace(binding.SelectedGroup) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return nil
	}
	value := CodexRecentRootChannelBinding{RootID: rootID, Binding: binding}
	return getCodexRecentRootChannelCache().SetWithTTL(key, value, codexRecentRootChannelCacheTTL)
}

func LoadRecentCodexRootChannelBinding(userID, tokenID int) (CodexRecentRootChannelBinding, bool, error) {
	key := codexRecentRootChannelCacheKey(userID, tokenID)
	if key == "" {
		return CodexRecentRootChannelBinding{}, false, nil
	}
	return getCodexRecentRootChannelCache().Get(key)
}

func codexRecentUserGroupChannelCacheKey(userID int, group string) string {
	group = strings.TrimSpace(group)
	if userID <= 0 || group == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + group))
	return hex.EncodeToString(digest[:])
}

// StoreRecentCodexUserGroupChannelBinding keeps a very short fallback for
// Codex-owned passive requests whose reviewer transport uses a different API
// token or omits the stable root graph. The lookup remains scoped to the same
// authenticated NewAPI user and selected group. RootID may be empty; only a
// tightly classified Guardian request is allowed to supply its reviewed root.
func StoreRecentCodexUserGroupChannelBinding(userID int, group, rootID string, binding CodexRootChannelBinding) error {
	key := codexRecentUserGroupChannelCacheKey(userID, group)
	rootID = strings.TrimSpace(rootID)
	if key == "" || binding.ChannelID <= 0 || strings.TrimSpace(binding.SelectedGroup) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return nil
	}
	value := CodexRecentRootChannelBinding{RootID: rootID, Binding: binding}
	return getCodexRecentUserGroupChannelCache().SetWithTTL(key, value, codexRecentRootChannelCacheTTL)
}

func LoadRecentCodexUserGroupChannelBinding(userID int, group string) (CodexRecentRootChannelBinding, bool, error) {
	key := codexRecentUserGroupChannelCacheKey(userID, group)
	if key == "" {
		return CodexRecentRootChannelBinding{}, false, nil
	}
	return getCodexRecentUserGroupChannelCache().Get(key)
}
