package perfmetrics

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestBuildUserAnomalyItemRequiresMinimumSamples(t *testing.T) {
	aggregate := model.PerfUserSampleAggregate{
		UserId:             7,
		Group:              "pro",
		RequestCount:       9,
		ErrorCount:         9,
		TtftCount:          9,
		TtftSumMs:          90000,
		AboveGroupAvgCount: 9,
	}

	_, ok := buildUserAnomalyItem(aggregate, 3000, 10, 5)
	require.False(t, ok)
}

func TestBuildUserAnomalyItemFlagsHalfOfTtftSamplesAboveGroupAverage(t *testing.T) {
	aggregate := model.PerfUserSampleAggregate{
		UserId:             7,
		Group:              "pro",
		RequestCount:       10,
		TtftCount:          10,
		TtftSumMs:          40000,
		AboveGroupAvgCount: 5,
	}

	item, ok := buildUserAnomalyItem(aggregate, 3000, 10, 5)
	require.True(t, ok)
	require.True(t, item.TtftAnomaly)
	require.False(t, item.ErrorAnomaly)
	require.Equal(t, 50.0, item.AboveGroupAvgPercentage)
}

func TestBuildUserAnomalyItemFlagsConfiguredErrorRate(t *testing.T) {
	aggregate := model.PerfUserSampleAggregate{
		UserId:       7,
		Group:        "pro",
		RequestCount: 20,
		ErrorCount:   2,
	}

	item, ok := buildUserAnomalyItem(aggregate, 3000, 10, 5)
	require.True(t, ok)
	require.False(t, item.TtftAnomaly)
	require.True(t, item.ErrorAnomaly)
	require.Equal(t, 10.0, item.ErrorRate)
}

func TestAggregateUserMetricRowsKeepsSnapshotAndRollupCounts(t *testing.T) {
	rows := []model.PerfUserSample{
		{
			UserId: 7, Username: "alice", Group: "pro", LastSeenAt: 100,
			RequestCount: 10, ErrorCount: 1, TtftCount: 10, TtftSumMs: 30000,
			TtftHistogram: `{"10":5,"50":5}`, Ip: "203.0.113.7", AccessUrl: "https://chat.example.com",
		},
		{
			UserId: 7, Username: "alice", Group: "pro", LastSeenAt: 110,
			RequestCount: 5, ErrorCount: 1, TtftCount: 5, TtftSumMs: 10000,
			TtftHistogram: `{"20":5}`, Ip: "198.51.100.9", AccessUrl: "https://other.example.com",
		},
	}

	groups, users, err := aggregateUserMetricRows(rows)
	require.NoError(t, err)
	require.Equal(t, int64(15), users[userMetricIdentityKey{userID: 7, group: "pro"}].aggregate.RequestCount)
	require.Equal(t, int64(2), users[userMetricIdentityKey{userID: 7, group: "pro"}].aggregate.ErrorCount)
	require.Equal(t, "203.0.113.7", users[userMetricIdentityKey{userID: 7, group: "pro"}].aggregate.Ip)
	require.Equal(t, "https://chat.example.com", users[userMetricIdentityKey{userID: 7, group: "pro"}].aggregate.AccessUrl)
	require.Equal(t, int64(15), groups["pro"].TtftCount)
}

func TestIsRelaySampleSuccessMatchesVisibleStreamError(t *testing.T) {
	info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	require.True(t, IsRelaySampleSuccess(info))

	failed := &relaycommon.RelayInfo{IsStream: true, StreamStatus: relaycommon.NewStreamStatus()}
	failed.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, nil)
	require.False(t, IsRelaySampleSuccess(failed))
}

func TestUserAnomalyPaginationBounds(t *testing.T) {
	page, pageSize := normalizeUserAnomalyPage(0, 0)
	require.Equal(t, 1, page)
	require.Equal(t, defaultUserAnomalyPageSize, pageSize)

	page, pageSize = normalizeUserAnomalyPage(3, 1000)
	require.Equal(t, 3, page)
	require.Equal(t, maxUserAnomalyPageSize, pageSize)
}

func TestHistogramCountAboveAverageUsesCompactBuckets(t *testing.T) {
	histogram := map[int64]int64{10: 4, 30: 6}
	require.Equal(t, int64(6), histogramCountAboveAverage(histogram, 2000))
}
