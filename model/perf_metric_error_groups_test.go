package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerfMetricErrorGroupsSeparateUsersStatusesTypesCodesAndUncodedReasons(test *testing.T) {
	setupPerfMetricErrorTest(test)
	now := time.Now().Unix()
	items := []PerfMetricError{
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 429, ErrorType: "admission", ErrorCode: "busy"},
		{CreatedAt: now, UserId: 8, ModelName: "model", StatusCode: 429, ErrorType: "admission", ErrorCode: "busy"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 500, ErrorType: "admission", ErrorCode: "busy"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 429, ErrorType: "upstream", ErrorCode: "busy"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 429, ErrorType: "admission", ErrorCode: "limit"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 500, ErrorReason: "connection closed"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 500, ErrorReason: "read timeout"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 500, ErrorReason: "read timeout"},
	}
	require.NoError(test, DB.Create(&items).Error)
	page, err := ListPerfMetricErrors(PerfMetricErrorQuery{Grouped: true})
	require.NoError(test, err)
	assert.Equal(test, int64(7), page.Total)
	assert.Equal(test, int64(8), page.TotalOccurrences)
	require.Len(test, page.Items, 7)
	assert.Equal(test, int64(2), page.Items[0].OccurrenceCount)
	details, err := ListPerfMetricErrors(PerfMetricErrorQuery{ErrorGroupID: page.Items[0].ErrorGroupID})
	require.NoError(test, err)
	require.Len(test, details.Items, 2)
	assert.Equal(test, "read timeout", details.Items[0].ErrorReason)
	assert.Equal(test, "read timeout", details.Items[1].ErrorReason)
}

func TestPerfMetricErrorGroupKeepsLatestRequestAndSnapshotWhenWritesArriveOutOfOrder(test *testing.T) {
	setupPerfMetricErrorTest(test)
	now := time.Now().Unix()
	items := []PerfMetricError{
		{CreatedAt: now, UserId: 7, ModelName: "gpt-a", StatusCode: 500, ErrorCode: "overloaded", RequestId: "latest-time"},
		{CreatedAt: now - 5, UserId: 7, ModelName: "gpt-b", StatusCode: 500, ErrorCode: "overloaded", RequestId: "late-write"},
	}
	require.NoError(test, DB.Create(&items).Error)
	page, err := ListPerfMetricErrors(PerfMetricErrorQuery{Grouped: true})
	require.NoError(test, err)
	require.Len(test, page.Items, 1)
	summary := page.Items[0]
	assert.Equal(test, "latest-time", summary.RequestId)
	assert.Equal(test, items[0].Id, summary.Id)
	assert.Equal(test, items[1].Id, summary.ErrorGroupID)
	assert.Equal(test, now-5, summary.FirstSeen)
	assert.Equal(test, now, summary.LastSeen)
	future := PerfMetricError{CreatedAt: now + 1, UserId: 7, ModelName: "gpt-a", StatusCode: 500, ErrorCode: "overloaded", RequestId: "after-snapshot"}
	require.NoError(test, DB.Create(&future).Error)
	details, err := ListPerfMetricErrors(PerfMetricErrorQuery{ErrorGroupID: summary.ErrorGroupID})
	require.NoError(test, err)
	assert.Equal(test, int64(2), details.Total)
	refreshed, err := ListPerfMetricErrors(PerfMetricErrorQuery{Grouped: true})
	require.NoError(test, err)
	require.Len(test, refreshed.Items, 1)
	assert.Equal(test, int64(3), refreshed.Items[0].OccurrenceCount)
	assert.Equal(test, summary.GroupKey, refreshed.Items[0].GroupKey)
}

func TestPerfMetricErrorGroupsRespectRetentionFiltersAndHiddenWindowLimits(test *testing.T) {
	setupPerfMetricErrorTest(test)
	now := time.Now().Unix()
	items := []PerfMetricError{
		{CreatedAt: now - 49*3600, UserId: 7, ModelName: "model", StatusCode: 500, ErrorCode: "overloaded"},
		{CreatedAt: now, UserId: 7, ModelName: "model", StatusCode: 400, ErrorCode: "session_creation_limit_exceeded"},
		{CreatedAt: now, UserId: 7, ModelName: "model", Group: "pro", StatusCode: 500, ErrorCode: "overloaded"},
		{CreatedAt: now, UserId: 7, ModelName: "model", Group: "standard", StatusCode: 500, ErrorCode: "overloaded"},
	}
	require.NoError(test, DB.Create(&items).Error)
	page, err := ListPerfMetricErrors(PerfMetricErrorQuery{Grouped: true})
	require.NoError(test, err)
	assert.Equal(test, int64(1), page.Total)
	assert.Equal(test, int64(2), page.TotalOccurrences)
	filtered, err := ListPerfMetricErrors(PerfMetricErrorQuery{Grouped: true, Group: "pro"})
	require.NoError(test, err)
	require.Len(test, filtered.Items, 1)
	assert.Equal(test, int64(1), filtered.Items[0].OccurrenceCount)
	empty, err := ListPerfMetricErrors(PerfMetricErrorQuery{Grouped: true, StartIndex: 100})
	require.NoError(test, err)
	assert.Equal(test, int64(1), empty.Total)
	assert.Empty(test, empty.Items)
	expired, err := ListPerfMetricErrors(PerfMetricErrorQuery{ErrorGroupID: items[0].Id})
	require.NoError(test, err)
	assert.Empty(test, expired.Items)
}
