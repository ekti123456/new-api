package perf_metrics_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	DefaultUserAnomalyMinRequests       = 10
	DefaultUserErrorRateThreshold       = 5.0
	DefaultUserTtftOverAveragePct       = 50.0
	DefaultUserErrorRateLockMinRequests = 100
	DefaultUserErrorRateLockThreshold   = 50.0
	DefaultUserErrorRateLockSeconds     = 60
	UserTtftAboveAverageRatio           = 50.0
	UserAnomalyRetentionHours           = 2
)

type PerfMetricsSetting struct {
	Enabled                      bool     `json:"enabled"`
	FlushInterval                int      `json:"flush_interval"`
	BucketTime                   string   `json:"bucket_time"`
	RetentionDays                int      `json:"retention_days"`
	UserAnomalyMonitoredGroups   []string `json:"user_anomaly_monitored_groups"`
	UserAnomalyMinRequests       int      `json:"user_anomaly_min_requests"`
	UserErrorRateThreshold       float64  `json:"user_error_rate_threshold"`
	UserTtftOverAveragePercent   float64  `json:"user_ttft_over_average_percent"`
	UserErrorRateLockEnabled     bool     `json:"user_error_rate_lock_enabled"`
	UserErrorRateLockMinRequests int      `json:"user_error_rate_lock_min_requests"`
	UserErrorRateLockThreshold   float64  `json:"user_error_rate_lock_threshold"`
	UserErrorRateLockSeconds     int      `json:"user_error_rate_lock_seconds"`
}

var perfMetricsSetting = PerfMetricsSetting{
	Enabled:                      true,
	FlushInterval:                5,
	BucketTime:                   "hour",
	RetentionDays:                0,
	UserAnomalyMonitoredGroups:   []string{},
	UserAnomalyMinRequests:       DefaultUserAnomalyMinRequests,
	UserErrorRateThreshold:       DefaultUserErrorRateThreshold,
	UserTtftOverAveragePercent:   DefaultUserTtftOverAveragePct,
	UserErrorRateLockEnabled:     false,
	UserErrorRateLockMinRequests: DefaultUserErrorRateLockMinRequests,
	UserErrorRateLockThreshold:   DefaultUserErrorRateLockThreshold,
	UserErrorRateLockSeconds:     DefaultUserErrorRateLockSeconds,
}

func init() {
	config.GlobalConfig.Register("perf_metrics_setting", &perfMetricsSetting)
}

func GetSetting() PerfMetricsSetting {
	return perfMetricsSetting
}

func GetBucketSeconds() int64 {
	switch perfMetricsSetting.BucketTime {
	case "minute":
		return 60
	case "5min":
		return 300
	case "hour":
		return 3600
	default:
		return 3600
	}
}

func GetFlushIntervalMinutes() int {
	if perfMetricsSetting.FlushInterval < 1 {
		return 1
	}
	return perfMetricsSetting.FlushInterval
}

func GetUserAnomalyMonitoredGroups() []string {
	groups := make([]string, 0, len(perfMetricsSetting.UserAnomalyMonitoredGroups))
	seen := make(map[string]struct{}, len(perfMetricsSetting.UserAnomalyMonitoredGroups))
	for _, group := range perfMetricsSetting.UserAnomalyMonitoredGroups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups
}

func IsUserAnomalyGroupMonitored(group string) bool {
	group = strings.TrimSpace(group)
	if group == "" {
		return false
	}
	for _, monitoredGroup := range GetUserAnomalyMonitoredGroups() {
		if monitoredGroup == group {
			return true
		}
	}
	return false
}

func GetUserAnomalyMinRequests() int {
	if perfMetricsSetting.UserAnomalyMinRequests < 1 {
		return DefaultUserAnomalyMinRequests
	}
	return perfMetricsSetting.UserAnomalyMinRequests
}

func GetUserErrorRateThreshold() float64 {
	threshold := perfMetricsSetting.UserErrorRateThreshold
	if threshold <= 0 || threshold > 100 {
		return DefaultUserErrorRateThreshold
	}
	return threshold
}

func GetUserTtftOverAveragePercent() float64 {
	percent := perfMetricsSetting.UserTtftOverAveragePercent
	if percent < 0 || percent > 1000 {
		return DefaultUserTtftOverAveragePct
	}
	return percent
}

func IsUserErrorRateLockEnabled() bool {
	return perfMetricsSetting.Enabled && perfMetricsSetting.UserErrorRateLockEnabled
}

func GetUserErrorRateLockMinRequests() int {
	value := perfMetricsSetting.UserErrorRateLockMinRequests
	if value < 1 || value > 100000 {
		return DefaultUserErrorRateLockMinRequests
	}
	return value
}

func GetUserErrorRateLockThreshold() float64 {
	value := perfMetricsSetting.UserErrorRateLockThreshold
	if value <= 0 || value > 100 {
		return DefaultUserErrorRateLockThreshold
	}
	return value
}

func GetUserErrorRateLockSeconds() int {
	value := perfMetricsSetting.UserErrorRateLockSeconds
	if value < 1 || value > 86400 {
		return DefaultUserErrorRateLockSeconds
	}
	return value
}
