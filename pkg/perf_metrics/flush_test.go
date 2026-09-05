package perfmetrics

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCleanupExpiredMetricsDoesNotShortenErrorRetention(test *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(test, err)
	sqlDB, err := database.DB()
	require.NoError(test, err)
	sqlDB.SetMaxOpenConns(1)
	originalDB := model.DB
	model.DB = database
	test.Cleanup(func() {
		model.DB = originalDB
		require.NoError(test, sqlDB.Close())
	})
	require.NoError(test, database.AutoMigrate(&model.PerfMetric{}, &model.PerfMetricError{}))
	createdAt := time.Now().Add(-36 * time.Hour).Unix()
	require.NoError(test, database.Create(&model.PerfMetric{
		ModelName: "gpt-a", Group: "default", BucketTs: createdAt, RequestCount: 1,
	}).Error)
	require.NoError(test, database.Create(&model.PerfMetricError{
		ModelName: "gpt-a", CreatedAt: createdAt, ErrorReason: "retained",
	}).Error)

	cleanupExpiredMetrics(0)
	var metricCount int64
	require.NoError(test, database.Model(&model.PerfMetric{}).Count(&metricCount).Error)
	assert.Equal(test, int64(1), metricCount)

	cleanupExpiredMetrics(1)
	require.NoError(test, database.Model(&model.PerfMetric{}).Count(&metricCount).Error)
	assert.Zero(test, metricCount)
	var errorCount int64
	require.NoError(test, database.Model(&model.PerfMetricError{}).Count(&errorCount).Error)
	assert.Equal(test, int64(1), errorCount)
}
