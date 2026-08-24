package perf_metrics_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserAnomalySettingsNormalizeGroupsAndFallbacks(t *testing.T) {
	original := perfMetricsSetting
	t.Cleanup(func() { perfMetricsSetting = original })

	perfMetricsSetting.UserAnomalyMonitoredGroups = []string{" pro ", "", "pro", "default"}
	perfMetricsSetting.UserAnomalyMinRequests = 0
	perfMetricsSetting.UserErrorRateThreshold = 0
	perfMetricsSetting.UserTtftOverAveragePercent = -1
	perfMetricsSetting.UserErrorRateLockEnabled = true
	perfMetricsSetting.UserErrorRateLockMinRequests = 0
	perfMetricsSetting.UserErrorRateLockThreshold = 101
	perfMetricsSetting.UserErrorRateLockSeconds = 0

	require.Equal(t, []string{"pro", "default"}, GetUserAnomalyMonitoredGroups())
	require.True(t, IsUserAnomalyGroupMonitored("pro"))
	require.False(t, IsUserAnomalyGroupMonitored("vip"))
	require.Equal(t, DefaultUserAnomalyMinRequests, GetUserAnomalyMinRequests())
	require.Equal(t, DefaultUserErrorRateThreshold, GetUserErrorRateThreshold())
	require.Equal(t, DefaultUserTtftOverAveragePct, GetUserTtftOverAveragePercent())
	require.True(t, IsUserErrorRateLockEnabled())
	require.Equal(t, DefaultUserErrorRateLockMinRequests, GetUserErrorRateLockMinRequests())
	require.Equal(t, DefaultUserErrorRateLockThreshold, GetUserErrorRateLockThreshold())
	require.Equal(t, DefaultUserErrorRateLockSeconds, GetUserErrorRateLockSeconds())

	perfMetricsSetting.UserTtftOverAveragePercent = 75
	require.Equal(t, 75.0, GetUserTtftOverAveragePercent())
}

func TestUserErrorRateLockRequiresPerformanceMetrics(t *testing.T) {
	original := perfMetricsSetting
	t.Cleanup(func() { perfMetricsSetting = original })

	perfMetricsSetting.UserErrorRateLockEnabled = true
	perfMetricsSetting.Enabled = false

	require.False(t, IsUserErrorRateLockEnabled())
}
