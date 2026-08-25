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
	codexRootChannelCacheNamespace = "new-api:codex_root_channel:v1"
	codexRootChannelCacheTTL       = 24 * time.Hour
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

var (
	codexRootChannelCacheOnce sync.Once
	codexRootChannelCache     *cachex.HybridCache[CodexRootChannelBinding]
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

func codexRootChannelCacheKey(userID int, rootID string) string {
	rootID = strings.TrimSpace(rootID)
	if userID <= 0 || rootID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(strconv.Itoa(userID) + "\x00" + rootID))
	return hex.EncodeToString(digest[:])
}

func StoreCodexRootChannelBinding(userID int, rootID string, binding CodexRootChannelBinding) error {
	key := codexRootChannelCacheKey(userID, rootID)
	if key == "" || binding.ChannelID <= 0 || strings.TrimSpace(binding.SelectedGroup) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return nil
	}
	return getCodexRootChannelCache().SetWithTTL(key, binding, codexRootChannelCacheTTL)
}

func LoadCodexRootChannelBinding(userID int, rootID string) (CodexRootChannelBinding, bool, error) {
	key := codexRootChannelCacheKey(userID, rootID)
	if key == "" {
		return CodexRootChannelBinding{}, false, nil
	}
	return getCodexRootChannelCache().Get(key)
}
