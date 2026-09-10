package controller

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

type personalWindowPool struct {
	Reference string                           `json:"reference,omitempty"`
	ID        int                              `json:"id"`
	Name      string                           `json:"name"`
	Available bool                             `json:"available"`
	Error     string                           `json:"error,omitempty"`
	Status    relaychannel.WindowControlResult `json:"status"`
}
type personalWindowCacheEntry struct {
	pools []personalWindowPool
	at    time.Time
}

func visiblePersonalWindowPools(pools []personalWindowPool, admin bool) []personalWindowPool {
	if admin {
		return pools
	}
	result := make([]personalWindowPool, len(pools))
	for index, pool := range pools {
		pool.ID = index + 1
		pool.Name = fmt.Sprintf("Codex2API %d", index+1)
		result[index] = pool
	}
	return result
}

var personalWindowCache = struct {
	sync.Mutex
	items map[string]personalWindowCacheEntry
}{items: make(map[string]personalWindowCacheEntry)}
var personalWindowFlight singleflight.Group

func GetPersonalWindows(requestContext *gin.Context) {
	userID := requestContext.GetInt("id")
	setting, err := model.GetUserSetting(userID, false)
	if err != nil {
		common.ApiError(requestContext, err)
		return
	}
	policy := operation_setting.GetWindowExpansionPolicy()
	key := fmt.Sprintf("%d:%v", userID, policy.ChannelIDs)
	personalWindowCache.Lock()
	entry, found := personalWindowCache.items[key]
	personalWindowCache.Unlock()
	if !found || time.Since(entry.at) >= 15*time.Second {
		value, fetchErr, _ := personalWindowFlight.Do(key, func() (interface{}, error) {
			pools := fetchPersonalWindowPools(requestContext, userID, policy.ChannelIDs)
			fresh := personalWindowCacheEntry{pools: pools, at: time.Now().UTC()}
			personalWindowCache.Lock()
			if len(personalWindowCache.items) >= 4096 {
				clear(personalWindowCache.items)
			}
			personalWindowCache.items[key] = fresh
			personalWindowCache.Unlock()
			return fresh, nil
		})
		if fetchErr != nil {
			common.ApiError(requestContext, fetchErr)
			return
		}
		entry = value.(personalWindowCacheEntry)
	}
	admin := requestContext.GetInt("role") >= common.RoleAdminUser
	if !admin {
		policy.ChannelIDs = []int{}
	}
	requestContext.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"enabled":             setting.WindowExpansionEnabled,
		"accepted_multiplier": setting.WindowExpansionAcceptedRatio,
		"policy":              policy,
		"pools":               visiblePersonalWindowPools(entry.pools, admin),
		"updated_at":          entry.at,
		"server_now":          time.Now().UTC(),
	}})
}

func fetchPersonalWindowPools(requestContext *gin.Context, userID int, channelIDs []int) []personalWindowPool {
	ctx, cancel := context.WithTimeout(requestContext.Request.Context(), 2500*time.Millisecond)
	defer cancel()
	type target struct {
		pool personalWindowPool
		info *relaycommon.RelayInfo
	}
	targets := make([]target, 0, len(channelIDs))
	seen := make(map[string]bool)
	for _, channelID := range channelIDs {
		item := target{pool: personalWindowPool{ID: channelID, Name: fmt.Sprintf("Pool %d", channelID)}}
		account, err := model.CacheGetChannel(channelID)
		if err != nil || account == nil {
			item.pool.Error = "Window service unavailable"
			targets = append(targets, item)
			continue
		}
		item.pool.Name = account.Name
		key := ""
		for index := range account.GetKeys() {
			if index >= 32 {
				break
			}
			candidate, keyErr := account.GetEnabledKeyAt(index)
			if keyErr == nil && relaychannel.IsCodex2APIPolicyDestination(account.GetBaseURL(), candidate) {
				key = candidate
				break
			}
		}
		if key == "" {
			item.pool.Error = "Verified Codex2API integration required"
		} else {
			item.info = &relaycommon.RelayInfo{UserId: userID, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: account.Id, ChannelBaseUrl: account.GetBaseURL(), ApiKey: key, ChannelSetting: account.GetSetting()}}
			identity, identityErr := relaychannel.WindowServiceIdentity(item.info)
			if identityErr != nil {
				item.pool.Error = "Verified Codex2API integration required"
				item.info = nil
				targets = append(targets, item)
				continue
			}
			if seen[identity] {
				continue
			}
			seen[identity] = true
			item.pool.Reference, _ = relaychannel.WindowServiceReference(item.info)
		}
		targets = append(targets, item)
	}
	pools := make([]personalWindowPool, len(targets))
	semaphore := make(chan struct{}, 4)
	var workers sync.WaitGroup
	for index, item := range targets {
		workers.Add(1)
		go func() {
			defer workers.Done()
			pools[index] = item.pool
			if item.info == nil {
				return
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				pools[index].Error = "Window service unavailable"
				return
			}
			defer func() { <-semaphore }()
			request := requestContext.Copy()
			request.Request = requestContext.Request.Clone(ctx)
			status, err := relaychannel.RequestUserWindows(request, item.info, relaychannel.WindowControlInput{Operation: "list", Multiplier: 1})
			if err != nil {
				pools[index].Error = "Window service unavailable"
				return
			}
			status.Ticket = ""
			pools[index].Available, pools[index].Status = true, status
		}()
	}
	workers.Wait()
	return pools
}

func SetPersonalWindowExpansion(requestContext *gin.Context) {
	var input struct {
		Enabled            bool    `json:"enabled"`
		AcceptedMultiplier float64 `json:"accepted_multiplier"`
	}
	if requestContext.ShouldBindJSON(&input) != nil {
		common.ApiErrorMsg(requestContext, "Invalid expansion preference")
		return
	}
	policy := operation_setting.GetWindowExpansionPolicy()
	if input.Enabled && (!policy.Enabled || input.AcceptedMultiplier != policy.Multiplier) {
		common.ApiErrorMsg(requestContext, "Expansion price changed. Refresh and confirm the current price.")
		return
	}
	if err := model.UpdateUserWindowExpansion(requestContext.GetInt("id"), input.Enabled, policy.Multiplier); err != nil {
		common.ApiError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, gin.H{"success": true})
}

func UpdateWindowExpansionPolicy(requestContext *gin.Context) {
	var policy operation_setting.WindowExpansionPolicy
	if requestContext.ShouldBindJSON(&policy) != nil {
		common.ApiErrorMsg(requestContext, "Invalid expansion policy")
		return
	}
	if err := policy.Validate(); err != nil {
		common.ApiError(requestContext, err)
		return
	}
	for _, channelID := range policy.ChannelIDs {
		account, err := model.CacheGetChannel(channelID)
		if err != nil || account == nil {
			common.ApiErrorMsg(requestContext, "Unknown channel")
			return
		}
		matched := false
		for _, key := range account.GetKeys() {
			if relaychannel.IsCodex2APIPolicyDestination(account.GetBaseURL(), key) {
				matched = true
				break
			}
		}
		if !matched {
			common.ApiErrorMsg(requestContext, "Verified Codex2API integration required")
			return
		}
	}
	encoded, err := common.Marshal(policy)
	if err == nil {
		err = model.UpdateOption("window_expansion_setting.policy", string(encoded))
	}
	if err != nil {
		common.ApiError(requestContext, err)
		return
	}
	requestContext.JSON(http.StatusOK, gin.H{"success": true})
}
