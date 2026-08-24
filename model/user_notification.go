package model

import (
	"time"

	"gorm.io/gorm"
)

const (
	userNotificationListLimit      = 100
	userNotificationRetentionLimit = 100
	userNotificationRetentionDays  = 7
)

var userNotificationNow = time.Now

// UserNotification is a private, in-app message visible only to its target
// user. It is intentionally separate from global notices and announcements.
type UserNotification struct {
	Id        int64  `json:"id" gorm:"primaryKey"`
	UserId    int    `json:"user_id" gorm:"index:idx_user_notification_user_time,priority:1;index:idx_user_notification_unread,priority:1"`
	Title     string `json:"title" gorm:"size:200;not null"`
	Content   string `json:"content" gorm:"type:text;not null"`
	CreatedBy int    `json:"-" gorm:"index"`
	CreatedAt int64  `json:"created_at" gorm:"index:idx_user_notification_user_time,priority:2"`
	ReadAt    int64  `json:"read_at" gorm:"default:0;index:idx_user_notification_unread,priority:2"`
}

func (UserNotification) TableName() string {
	return "user_notifications"
}

func CreateUserNotification(notification *UserNotification) error {
	if notification.CreatedAt <= 0 {
		notification.CreatedAt = userNotificationNow().Unix()
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := deleteExpiredUserNotifications(tx, notification.UserId, userNotificationNow().Unix()); err != nil {
			return err
		}
		if err := tx.Create(notification).Error; err != nil {
			return err
		}
		var staleIds []int64
		if err := tx.Model(&UserNotification{}).
			Where("user_id = ?", notification.UserId).
			Order("id DESC").
			Limit(userNotificationRetentionLimit).
			Offset(userNotificationRetentionLimit).
			Pluck("id", &staleIds).Error; err != nil {
			return err
		}
		if len(staleIds) > 0 {
			if err := tx.Where("id IN ?", staleIds).Delete(&UserNotification{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func ListUserNotifications(userId int) ([]UserNotification, int64, error) {
	items := make([]UserNotification, 0)
	var unreadCount int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := deleteExpiredUserNotifications(tx, userId, userNotificationNow().Unix()); err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userId).
			Order("created_at DESC, id DESC").
			Limit(userNotificationListLimit).
			Find(&items).Error; err != nil {
			return err
		}
		return tx.Model(&UserNotification{}).
			Where("user_id = ? AND read_at = 0", userId).
			Count(&unreadCount).Error
	})
	if err != nil {
		return nil, 0, err
	}
	return items, unreadCount, nil
}

func MarkUserNotificationsRead(userId int) error {
	now := userNotificationNow().Unix()
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := deleteExpiredUserNotifications(tx, userId, now); err != nil {
			return err
		}
		return tx.Model(&UserNotification{}).
			Where("user_id = ? AND read_at = 0", userId).
			Update("read_at", now).Error
	})
}

func userNotificationCutoff(now int64) int64 {
	return now - int64(userNotificationRetentionDays*24*time.Hour/time.Second)
}

func deleteExpiredUserNotifications(tx *gorm.DB, userId int, now int64) error {
	return tx.Where("user_id = ? AND created_at < ?", userId, userNotificationCutoff(now)).
		Delete(&UserNotification{}).Error
}

// DeleteExpiredUserNotifications removes expired rows for inactive users as
// well. Active users are also cleaned on create/list, so the seven-day
// retention policy remains effective even if the periodic job is delayed.
func DeleteExpiredUserNotifications(now int64) error {
	if DB == nil || !DB.Migrator().HasTable(&UserNotification{}) {
		return nil
	}
	return DB.Where("created_at < ?", userNotificationCutoff(now)).
		Delete(&UserNotification{}).Error
}
