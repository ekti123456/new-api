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

	require.Equal(t, []string{"pro", "default"}, GetUserAnomalyMonitoredGroups())
	require.True(t, IsUserAnomalyGroupMonitored("pro"))
	require.False(t, IsUserAnomalyGroupMonitored("vip"))
	require.Equal(t, DefaultUserAnomalyMinRequests, GetUserAnomalyMinRequests())
	require.Equal(t, DefaultUserErrorRateThreshold, GetUserErrorRateThreshold())
	require.Equal(t, DefaultUserTtftOverAveragePct, GetUserTtftOverAveragePercent())

	perfMetricsSetting.UserTtftOverAveragePercent = 75
	require.Equal(t, 75.0, GetUserTtftOverAveragePercent())
}
