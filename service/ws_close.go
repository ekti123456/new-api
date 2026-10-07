package service

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"gorm.io/gorm"
)

const ChannelDisabledCloseReason = "channel disabled or deleted"

// Called after an administrative channel mutation. Query only channels that
// currently own sockets; a database outage is not evidence of revocation.
func CloseUnavailableChannelWebSockets() {
	var revoked []int
	for _, id := range wsmanager.ActiveChannelIDs() {
		channel, err := model.GetChannelById(id, false)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && (channel.Status != common.ChannelStatusEnabled || !channel.GetSetting().ResponsesWebSocketEnabled)) {
			revoked = append(revoked, id)
		}
	}
	if len(revoked) > 0 {
		CloseActiveWebSocketsForChannels(revoked, ChannelDisabledCloseReason)
	}
}

func CloseActiveWebSocketsForChannel(channelID int, reason string) int {
	return wsmanager.CloseChannelsAndBroadcast([]int{channelID}, reason)
}

func CloseActiveWebSocketsForChannels(channelIDs []int, reason string) int {
	return wsmanager.CloseChannelsAndBroadcast(channelIDs, reason)
}
