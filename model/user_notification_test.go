package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserNotificationsAreIsolatedReadableAndBounded(t *testing.T) {
	truncateTables(t)
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

	require.NoError(t, MarkUserNotificationsRead(101))
	_, unreadCount, err = ListUserNotifications(101)
	require.NoError(t, err)
	require.Zero(t, unreadCount)

	_, otherUnreadCount, err := ListUserNotifications(202)
	require.NoError(t, err)
	require.Equal(t, int64(1), otherUnreadCount)
}
