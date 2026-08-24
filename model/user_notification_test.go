package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUserNotificationsAreIsolatedReadableAndBounded(t *testing.T) {
	truncateTables(t)
	now := time.Unix(1_800_000_000, 0)
	originalNow := userNotificationNow
	userNotificationNow = func() time.Time { return now }
	t.Cleanup(func() { userNotificationNow = originalNow })
	require.NoError(t, DB.Create(&UserNotification{
		UserId:    101,
		Title:     "expired",
		Content:   "expired",
		CreatedAt: now.Add(-8 * 24 * time.Hour).Unix(),
	}).Error)
	for index := 0; index < userNotificationRetentionLimit+1; index++ {
		require.NoError(t, CreateUserNotification(&UserNotification{
			UserId:  101,
			Title:   fmt.Sprintf("notice-%d", index),
			Content: "content",
		}))
	}
	require.NoError(t, CreateUserNotification(&UserNotification{
		UserId:  202,
		Title:   "other-user",
		Content: "private",
	}))

	items, unreadCount, err := ListUserNotifications(101)
	require.NoError(t, err)
	require.Len(t, items, userNotificationListLimit)
	require.Equal(t, int64(userNotificationRetentionLimit), unreadCount)
	require.Equal(t, "notice-100", items[0].Title)

	var retainedCount int64
	require.NoError(t, DB.Model(&UserNotification{}).
		Where("user_id = ?", 101).
		Count(&retainedCount).Error)
	require.Equal(t, int64(userNotificationRetentionLimit), retainedCount)
	var expiredCount int64
	require.NoError(t, DB.Model(&UserNotification{}).
		Where("user_id = ? AND title = ?", 101, "expired").
		Count(&expiredCount).Error)
	require.Zero(t, expiredCount)

	require.NoError(t, MarkUserNotificationsRead(101))
	_, unreadCount, err = ListUserNotifications(101)
	require.NoError(t, err)
	require.Zero(t, unreadCount)

	_, otherUnreadCount, err := ListUserNotifications(202)
	require.NoError(t, err)
	require.Equal(t, int64(1), otherUnreadCount)
}

func TestDeleteExpiredUserNotificationsCleansInactiveUsers(t *testing.T) {
	truncateTables(t)
	now := time.Unix(1_800_000_000, 0)
	require.NoError(t, DB.Create(&UserNotification{
		UserId:    303,
		Title:     "expired",
		Content:   "expired",
		CreatedAt: now.Add(-8 * 24 * time.Hour).Unix(),
	}).Error)
	require.NoError(t, DB.Create(&UserNotification{
		UserId:    303,
		Title:     "retained",
		Content:   "retained",
		CreatedAt: now.Add(-6 * 24 * time.Hour).Unix(),
	}).Error)

	require.NoError(t, DeleteExpiredUserNotifications(now.Unix()))
	var items []UserNotification
	require.NoError(t, DB.Where("user_id = ?", 303).Order("id").Find(&items).Error)
	require.Len(t, items, 1)
	require.Equal(t, "retained", items[0].Title)
}
