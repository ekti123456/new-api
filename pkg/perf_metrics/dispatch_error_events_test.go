package perfmetrics

import (
	"errors"
	"net/http/httptest"
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

func TestDispatchErrorDiagnosticsStayIn48HourAdminStore(test *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(test, err)
	sqlDB, err := database.DB()
	require.NoError(test, err)
	sqlDB.SetMaxOpenConns(1)
	originalDB := model.DB
	setting := perf_metrics_setting.GetSetting()
	originalEnabled := setting.Enabled
	model.DB = database
	setting.Enabled = true
	test.Cleanup(func() { model.DB = originalDB; setting.Enabled = originalEnabled; require.NoError(test, sqlDB.Close()) })
	require.NoError(test, database.AutoMigrate(&model.PerfMetricError{}, &model.Log{}))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(common.RequestIdKey, "dispatch-admin")
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{UserId: 42, OriginModelName: "model", UsingGroup: "pro"}
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 7}
	apiError := types.NewErrorWithStatusCode(errors.New("Request temporarily unavailable"), types.ErrorCodeBadResponseStatusCode, 503)
	publicBefore, err := common.Marshal(apiError.ToOpenAIError())
	require.NoError(test, err)
	diagnostic := common.CodexDispatchDiagnostic{RequestID: "dispatch-admin", ChannelID: 7, Status: 503, Stage: "account_selection", Reason: "affinity_group_mismatch", Reasons: []string{"affinity_group_mismatch"}, Retry: "stop"}
	common.SetCodexDispatchDiagnostic(ctx, diagnostic)
	RecordRelayError(ctx, info, apiError)
	var records []model.PerfMetricError
	require.NoError(test, database.Find(&records).Error)
	require.Len(test, records, 1)
	assert.Equal(test, "codex_dispatch_affinity_group_mismatch", records[0].ErrorCode)
	assert.Contains(test, records[0].ErrorReason, "affinity_group_mismatch")
	publicAfter, err := common.Marshal(apiError.ToOpenAIError())
	require.NoError(test, err)
	assert.Equal(test, publicBefore, publicAfter)
	var userLogs int64
	require.NoError(test, database.Model(&model.Log{}).Count(&userLogs).Error)
	assert.Zero(test, userLogs)
	common.ClearCodexDispatchDiagnostic(ctx)
	RecordRelayError(ctx, info, apiError)
	require.NoError(test, database.Order("id").Find(&records).Error)
	require.Len(test, records, 2)
	assert.NotContains(test, records[1].ErrorReason, "affinity_group_mismatch")
	require.NoError(test, database.Model(&model.PerfMetricError{}).Where("id = ?", records[0].Id).Update("created_at", time.Now().Add(-49*time.Hour).Unix()).Error)
	require.NoError(test, model.DeleteExpiredPerfMetricErrors(time.Now()))
	require.NoError(test, database.Find(&records).Error)
	require.Len(test, records, 1)
	assert.NotContains(test, records[0].ErrorReason, "affinity_group_mismatch")
}
