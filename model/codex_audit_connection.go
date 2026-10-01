package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
)

// GetCodexAuditChannels is server-only. Calling credentials are used to verify
// a configured audit destination and must never appear in the editor response.
func GetCodexAuditChannels(ctx context.Context) ([]*Channel, error) {
	var rows []*Channel
	err := DB.WithContext(ctx).
		Select([]string{"id", "type", "key", "base_url", "status", "channel_info"}).
		Where("status = ?", common.ChannelStatusEnabled).Find(&rows).Error
	return rows, err
}
