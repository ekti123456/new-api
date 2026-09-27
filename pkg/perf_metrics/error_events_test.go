package perfmetrics

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestFinalPerformanceErrorsFollowHealthOutcome(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PerfMetricError{}))
	oldDB := model.DB
	require.True(t, perf_metrics_setting.GetSetting().Enabled)
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		conn, _ := db.DB()
		_ = conn.Close()
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", strings.Repeat("界", 300))
	c.Set(common.RequestIdKey, "final-request")
	info := &relaycommon.RelayInfo{UserId: 7, OriginModelName: "model", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 3}}
	// A successful retried request and a client/business rejection add no row.
	RecordRelayError(c, info, nil)
	RecordRelayError(c, info, types.InitOpenAIError("context_length_exceeded", 400))
	var count int64
	require.NoError(t, db.Model(&model.PerfMetricError{}).Count(&count).Error)
	assert.Zero(t, count)
	RecordRelayError(c, info, types.InitOpenAIError("rate_limit_exceeded", 429))
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.MarkFailed("upstream_stream_failed", "server_error", 503)
	RecordRelayError(c, info, nil)
	var rows []model.PerfMetricError
	require.NoError(t, db.Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, 429, rows[0].StatusCode)
	assert.Equal(t, 503, rows[1].StatusCode)
	assert.Equal(t, "final-request", rows[1].RequestId)
	assert.LessOrEqual(t, len(rows[0].UserAgent), 512)
	assert.True(t, utf8.ValidString(rows[0].UserAgent))
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	RecordRelayError(c, info, nil)
	require.NoError(t, db.Model(&model.PerfMetricError{}).Count(&count).Error)
	assert.Equal(t, int64(2), count)
}
