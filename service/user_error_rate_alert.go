package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/console_setting"
)

func observeAndNotifyUserErrorRateLock(relayInfo *relaycommon.RelayInfo, user *perfmetrics.UserMetricIdentity) {
	status := perfmetrics.ObserveUserErrorRate(relayInfo, user)
	if !status.Triggered || user == nil {
		return
	}
	notifyRootOfUserErrorRateLock(status, user)
}

func notifyRootOfUserErrorRateLock(status perfmetrics.UserErrorRateLockStatus, user *perfmetrics.UserMetricIdentity) {
	if user == nil {
		return
	}
	rootUser := model.GetRootUser()
	if rootUser == nil || rootUser.Id <= 0 {
		common.SysError("failed to notify root user about a user error-rate lock: root user not found")
		return
	}
	username := strings.TrimSpace(user.Username)
	if username == "" {
		username = fmt.Sprintf("#%d", user.UserID)
	}
	accessURL := strings.TrimSpace(status.AccessURL)
	if accessURL == "" {
		accessURL = "未知"
	}
	content := fmt.Sprintf(
		"用户：%s (#%d)\n分组：%s\n最近请求数：%d\n错误数：%d\n错误率：%.1f%%\n临时锁定：%d 秒\n本次访问 URL：%s\n\n可用访问地址：\n%s\n\n本轮统计已在触发锁定时重置，锁定结束后将重新统计。",
		username,
		user.UserID,
		status.Group,
		status.RequestCount,
		status.ErrorCount,
		status.ErrorRate,
		status.LockSeconds,
		accessURL,
		console_setting.FormatAPIInfoSummary(),
	)
	if err := model.CreateUserNotification(&model.UserNotification{
		UserId:  rootUser.Id,
		Title:   "用户高错误率已触发临时锁定",
		Content: content,
	}); err != nil {
		common.SysError(fmt.Sprintf("failed to notify root user about error-rate lock for user %d: %v", user.UserID, err))
	}
}
