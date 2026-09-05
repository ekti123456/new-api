package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPerfMetricErrorTest(test *testing.T) {
	test.Helper()
	require.NoError(test, DB.AutoMigrate(&PerfMetricError{}))
	require.NoError(test, DB.Where("1 = 1").Delete(&PerfMetricError{}).Error)
	test.Cleanup(func() {
		require.NoError(test, DB.Where("1 = 1").Delete(&PerfMetricError{}).Error)
	})
}

func TestListPerfMetricErrorsFiltersAndPaginates(test *testing.T) {
	setupPerfMetricErrorTest(test)
	now := time.Now().Unix()
	items := []PerfMetricError{
		{CreatedAt: now - 300, UserId: 1, Username: "alice", ModelName: "gpt-a", Group: "pro", StatusCode: 503, ErrorType: "upstream", ErrorCode: "no_channel", ErrorReason: "first"},
		{CreatedAt: now - 200, UserId: 2, Username: "bob", ModelName: "gpt-a", Group: "pro", StatusCode: 429, ErrorType: "upstream", ErrorCode: "rate_limited", ErrorReason: "second"},
		{CreatedAt: now - 100, UserId: 1, Username: "alice", ModelName: "gpt-b", Group: "default", StatusCode: 500, ErrorType: "transport", ErrorCode: "closed", ErrorReason: "third"},
	}
	require.NoError(test, DB.Create(&items).Error)

	page, err := ListPerfMetricErrors(PerfMetricErrorQuery{
		ModelName: "gpt-a", Group: "pro", StartIndex: 0, PageSize: 1,
	})
	require.NoError(test, err)
	assert.Equal(test, int64(2), page.Total)
	assert.Equal(test, 1, page.Page)
	require.Len(test, page.Items, 1)
	assert.Equal(test, "second", page.Items[0].ErrorReason)

	filtered, err := ListPerfMetricErrors(PerfMetricErrorQuery{
		Username: "alice", UserID: 1, StatusCode: 500, ErrorType: "transport", ErrorCode: "closed", StartIndex: 0, PageSize: 20,
	})
	require.NoError(test, err)
	assert.Equal(test, int64(1), filtered.Total)
	require.Len(test, filtered.Items, 1)
	assert.Equal(test, "gpt-b", filtered.Items[0].ModelName)
}

func TestListPerfMetricErrorsExcludesExpiredRowsBeforeCleanup(test *testing.T) {
	setupPerfMetricErrorTest(test)
	now := time.Now()
	items := []PerfMetricError{
		{CreatedAt: now.Add(-49 * time.Hour).Unix(), ModelName: "gpt-a", ErrorReason: "expired"},
		{CreatedAt: now.Add(-47 * time.Hour).Unix(), ModelName: "gpt-a", ErrorReason: "retained"},
	}
	require.NoError(test, DB.Create(&items).Error)

	for _, query := range []PerfMetricErrorQuery{
		{},
		{StartTimestamp: now.Add(-72 * time.Hour).Unix(), EndTimestamp: now.Unix()},
	} {
		page, err := ListPerfMetricErrors(query)
		require.NoError(test, err)
		assert.Equal(test, int64(1), page.Total)
		require.Len(test, page.Items, 1)
		assert.Equal(test, "retained", page.Items[0].ErrorReason)
	}

	page, err := ListPerfMetricErrors(PerfMetricErrorQuery{
		StartTimestamp: now.Add(-72 * time.Hour).Unix(),
		EndTimestamp:   now.Add(-49 * time.Hour).Unix(),
	})
	require.NoError(test, err)
	assert.Zero(test, page.Total)
	assert.Empty(test, page.Items)

	var storedCount int64
	require.NoError(test, DB.Model(&PerfMetricError{}).Count(&storedCount).Error)
	assert.Equal(test, int64(2), storedCount)
}

func TestDeleteExpiredPerfMetricErrorsPreserves48HourBoundaryAndOtherLogs(test *testing.T) {
	setupPerfMetricErrorTest(test)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-48 * time.Hour).Unix()
	expired := make([]PerfMetricError, 1001)
	for index := range expired {
		expired[index] = PerfMetricError{CreatedAt: cutoff - 1, ModelName: "gpt-a"}
	}
	require.NoError(test, DB.CreateInBatches(&expired, 25).Error)
	retained := []PerfMetricError{
		{CreatedAt: cutoff, ModelName: "gpt-a", ErrorReason: "boundary"},
		{CreatedAt: cutoff + 1, ModelName: "gpt-a", ErrorReason: "recent"},
	}
	require.NoError(test, DB.Create(&retained).Error)
	metric := PerfMetric{ModelName: "retention-test", Group: "default", BucketTs: cutoff - 1, RequestCount: 7}
	require.NoError(test, DB.Create(&metric).Error)
	billingLog := Log{CreatedAt: cutoff - 1, Type: LogTypeConsume, Content: "retention-test"}
	require.NoError(test, LOG_DB.Create(&billingLog).Error)
	test.Cleanup(func() {
		require.NoError(test, DB.Delete(&metric).Error)
		require.NoError(test, LOG_DB.Where("id = ?", billingLog.Id).Delete(&Log{}).Error)
	})

	require.NoError(test, DeleteExpiredPerfMetricErrors(now))
	require.NoError(test, DeleteExpiredPerfMetricErrors(now))
	var remaining []PerfMetricError
	require.NoError(test, DB.Order("created_at ASC").Find(&remaining).Error)
	assert.Equal(test, retained, remaining)
	var storedMetric PerfMetric
	require.NoError(test, DB.First(&storedMetric, metric.Id).Error)
	assert.Equal(test, int64(7), storedMetric.RequestCount)
	var storedLog Log
	require.NoError(test, LOG_DB.Where("id = ?", billingLog.Id).First(&storedLog).Error)
	assert.Equal(test, billingLog, storedLog)
}
