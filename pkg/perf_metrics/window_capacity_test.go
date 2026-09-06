package perfmetrics

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWindowCapacityFailuresDoNotAffectPerformance(test *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(test, err)
	sqlDB, err := database.DB()
	require.NoError(test, err)
	sqlDB.SetMaxOpenConns(1)
	originalDB, originalRedisEnabled := model.DB, common.RedisEnabled
	settings := perf_metrics_setting.GetSetting()
	originalEnabled := settings.Enabled
	model.DB, common.RedisEnabled, settings.Enabled = database, false, true
	test.Cleanup(func() {
		model.DB, common.RedisEnabled, settings.Enabled = originalDB, originalRedisEnabled, originalEnabled
		hotBuckets.Range(func(key, value any) bool {
			if strings.HasPrefix(key.(bucketKey).model, "capacity-test-") {
				hotBuckets.Delete(key)
			}
			return true
		})
		require.NoError(test, sqlDB.Close())
	})
	require.NoError(test, database.AutoMigrate(&model.PerfMetric{}, &model.PerfMetricError{}))
	for _, scenario := range []struct {
		name     string
		code     string
		status   int
		excluded bool
	}{
		{"user window", "session_creation_limit_exceeded", 400, true},
		{"legacy user window", "session_creation_limit_exceeded", 429, true},
		{"account window", "account_session_capacity_exceeded", 400, true},
		{"usage quota", "usage_limit_reached", 429, false},
		{"identity conflict", "session_identity_conflict", 400, false},
		{"bad request", "invalid_request_error", 400, false},
		{"server failure", "session_creation_limit_exceeded", 500, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			requestContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			requestContext.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			apiError := types.NewErrorWithStatusCode(errors.New("window or upstream failure"), types.ErrorCode(scenario.code), scenario.status)
			info := &relaycommon.RelayInfo{
				UserId: 42, OriginModelName: "capacity-test-" + scenario.name, UsingGroup: "pro",
				StartTime: time.Now(), LastError: apiError,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7},
			}
			before, marshalErr := common.Marshal(apiError.ToOpenAIError())
			require.NoError(test, marshalErr)
			RecordRelayError(requestContext, info, apiError)
			RecordRelaySample(info, false, 0, nil)
			var storedCount int64
			require.NoError(test, database.Model(&model.PerfMetricError{}).Where("model_name = ?", info.OriginModelName).Count(&storedCount).Error)
			if scenario.excluded {
				assert.Zero(test, storedCount)
				empty, queryErr := Query(QueryParams{Model: info.OriginModelName, Hours: 1})
				require.NoError(test, queryErr)
				assert.Empty(test, empty.Groups, "a rejected window must not manufacture a successful sample")
			} else {
				assert.Equal(test, int64(1), storedCount)
			}
			RecordRelaySample(info, true, 10, nil)
			result, queryErr := Query(QueryParams{Model: info.OriginModelName, Hours: 1})
			require.NoError(test, queryErr)
			require.Len(test, result.Groups, 1)
			if scenario.excluded {
				assert.Equal(test, 100.0, result.Groups[0].SuccessRate)
			} else {
				assert.Equal(test, 50.0, result.Groups[0].SuccessRate)
			}
			after, marshalErr := common.Marshal(apiError.ToOpenAIError())
			require.NoError(test, marshalErr)
			assert.Equal(test, before, after, "metrics must not rewrite the client error")
		})
	}
}
