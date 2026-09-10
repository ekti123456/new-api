package controller

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func UpgradePersonalWindow(request *gin.Context) {
	var input struct {
		PoolReference string  `json:"pool_reference"`
		Root          string  `json:"root"`
		GrantID       string  `json:"grant_id"`
		Multiplier    float64 `json:"accepted_multiplier"`
	}
	if request.ShouldBindJSON(&input) != nil || len(input.PoolReference) != 64 || input.Root == "" || len(input.Root) > 64 || input.GrantID == "" || len(input.GrantID) > 64 {
		common.ApiErrorMsg(request, "Invalid window expansion confirmation")
		return
	}
	userID := request.GetInt("id")
	preferences, err := model.GetUserSetting(userID, true)
	if err != nil {
		common.ApiError(request, err)
		return
	}
	policy := operation_setting.GetWindowExpansionPolicy()
	if !policy.Enabled || !preferences.WindowExpansionEnabled || preferences.WindowExpansionAcceptedRatio < policy.Multiplier || input.Multiplier != policy.Multiplier {
		common.ApiErrorMsg(request, "Enable expansion and confirm the current price before upgrading this window.")
		return
	}
	var destination *relaycommon.RelayInfo
	for _, channelID := range policy.ChannelIDs {
		channel, channelErr := model.CacheGetChannel(channelID)
		if channelErr != nil || channel == nil {
			continue
		}
		for index := range channel.GetKeys() {
			if index >= 32 {
				break
			}
			key, keyErr := channel.GetEnabledKeyAt(index)
			if keyErr != nil || !relaychannel.IsCodex2APIPolicyDestination(channel.GetBaseURL(), key) {
				continue
			}
			candidate := &relaycommon.RelayInfo{UserId: userID, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelBaseUrl: channel.GetBaseURL(), ApiKey: key, ChannelSetting: channel.GetSetting()}}
			reference, referenceErr := relaychannel.WindowServiceReference(candidate)
			if referenceErr == nil && reference == input.PoolReference {
				destination = candidate
			}
			break
		}
		if destination != nil {
			break
		}
	}
	if destination == nil {
		common.ApiErrorMsg(request, "Window service unavailable")
		return
	}
	_, err = relaychannel.RequestUserWindows(request, destination, relaychannel.WindowControlInput{Operation: "upgrade", Root: input.Root, GrantID: input.GrantID, AllowExpansion: true, ExtraLimit: policy.ExtraLimit, Multiplier: policy.Multiplier})
	if err != nil {
		common.ApiError(request, err)
		return
	}
	relaychannel.InvalidateUserWindowBilling(userID)
	personalWindowCache.Lock()
	for key := range personalWindowCache.items {
		if strings.HasPrefix(key, fmt.Sprintf("%d:", userID)) {
			delete(personalWindowCache.items, key)
		}
	}
	personalWindowCache.Unlock()
	request.JSON(http.StatusOK, gin.H{"success": true})
}
