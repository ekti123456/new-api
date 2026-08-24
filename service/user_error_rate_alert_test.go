package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotifyRootOfUserErrorRateLockCreatesPersonalNotification(t *testing.T) {
	originalDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserNotification{}))
	model.DB = db
	t.Cleanup(func() { model.DB = originalDB })

	rootUser := model.User{Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&rootUser).Error)

	consoleSetting := console_setting.GetConsoleSetting()
	originalConsoleSetting := *consoleSetting
	t.Cleanup(func() { *consoleSetting = originalConsoleSetting })
	consoleSetting.ApiInfo = `[
		{"url":"https://chat.example.com","route":"direct","description":"Direct route","color":"blue"},
		{"url":"https://cf-chat.example.com","route":"cdn","description":"CDN route","color":"green"}
	]`

	notifyRootOfUserErrorRateLock(perfmetrics.UserErrorRateLockStatus{
		Locked:       true,
		Triggered:    true,
		Group:        "pro",
		RequestCount: 120,
		ErrorCount:   72,
		ErrorRate:    60,
		LockSeconds:  60,
		AccessURL:    "https://cf-chat.example.com",
	}, &perfmetrics.UserMetricIdentity{UserID: 2993, Username: "tester"})

	items, unreadCount, err := model.ListUserNotifications(rootUser.Id)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(1), unreadCount)
	assert.Equal(t, "用户高错误率已触发临时锁定", items[0].Title)
	assert.Contains(t, items[0].Content, "用户：tester (#2993)")
	assert.Contains(t, items[0].Content, "本次访问 URL：https://cf-chat.example.com")
	assert.Contains(t, items[0].Content, "URL：https://chat.example.com")
	assert.Contains(t, items[0].Content, "说明信息：Direct route")
	assert.Contains(t, items[0].Content, "URL：https://cf-chat.example.com")
	assert.Contains(t, items[0].Content, "说明信息：CDN route")
}
