package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayAdmissionErrorStoresUserLogWhenAdminDatabaseUnavailable(test *testing.T) {
	truncateTables(test)
	originalDB := DB
	DB = nil
	test.Cleanup(func() { DB = originalDB })
	err := CreateRelayAdmissionError(test.Context(), &PerfMetricError{UserId: 42}, &Log{
		UserId: 42, Type: LogTypeError, RequestId: "admission-separate-log-db",
	})
	require.ErrorContains(test, err, "admission audit database unavailable")
	var entry Log
	require.NoError(test, LOG_DB.Where("request_id = ?", "admission-separate-log-db").First(&entry).Error)
	assert.Equal(test, 42, entry.UserId)
	assert.Zero(test, entry.Quota)
}

func TestRelayAdmissionErrorHonorsWriteCancellation(test *testing.T) {
	truncateTables(test)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := CreateRelayAdmissionError(ctx, &PerfMetricError{UserId: 42, RequestId: "cancelled-admission"}, &Log{
		UserId: 42, Type: LogTypeError, RequestId: "cancelled-admission",
	})
	assert.ErrorIs(test, err, context.Canceled)
	var count int64
	require.NoError(test, DB.Model(&PerfMetricError{}).Where("request_id = ?", "cancelled-admission").Count(&count).Error)
	assert.Zero(test, count)
	require.NoError(test, LOG_DB.Model(&Log{}).Where("request_id = ?", "cancelled-admission").Count(&count).Error)
	assert.Zero(test, count)
}
