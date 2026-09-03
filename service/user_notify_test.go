package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestNotifyUserUsesDailyDatabaseClaimOnlyForQuotaWarnings(t *testing.T) {
	truncate(t)
	originalLimitCount := constant.NotifyLimitCount
	originalLimitDuration := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 2
	constant.NotificationLimitDurationMinute = 10
	t.Cleanup(func() {
		constant.NotifyLimitCount = originalLimitCount
		constant.NotificationLimitDurationMinute = originalLimitDuration
	})
	user := model.User{
		Id:       4301,
		Username: "quota-notify-service",
		AffCode:  "quota-notify-aff-service",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&user).Error)

	quotaWarning := dto.NewNotify(dto.NotifyTypeQuotaExceed, "quota", "quota", nil)
	require.NoError(t, NotifyUser(user.Id, "", dto.UserSetting{}, quotaWarning))

	var claimedAt int64
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", user.Id).
		Select("last_quota_notify_at").
		Scan(&claimedAt).Error)
	require.Positive(t, claimedAt)

	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", user.Id).
		Update("last_quota_notify_at", 0).Error)
	otherNotification := dto.NewNotify(dto.NotifyTypeChannelTest, "channel", "channel", nil)
	require.NoError(t, NotifyUser(user.Id, "", dto.UserSetting{}, otherNotification))

	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", user.Id).
		Select("last_quota_notify_at").
		Scan(&claimedAt).Error)
	require.Zero(t, claimedAt)
}
