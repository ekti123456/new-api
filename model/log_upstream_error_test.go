package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http/httptest"
	"testing"
)

func TestUpstreamErrorLogsAreAdminOnlyAndAttemptScoped(t *testing.T) {
	truncateTables(t)
	previousLog, previousExport := common.LogConsumeEnabled, common.DataExportEnabled
	t.Cleanup(func() { common.LogConsumeEnabled, common.DataExportEnabled = previousLog, previousExport })
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set(common.RequestIdKey, "cause-request")
	attempt := common.CodexUpstreamErrorAttemptForContext(c)
	d := common.CodexUpstreamError{RequestID: "cause-request", ChannelID: 7, Message: "connection reset by peer", Source: "transport", Stage: "ws_read"}
	attempt.Record(d)
	RecordErrorLog(c, 1, 7, "model", "", "status_code=500, generic public message", 0, 1, true, "", nil)
	RecordConsumeLog(c, 1, RecordConsumeLogParams{ChannelId: 7, ModelName: "model", IsStream: true})
	common.ClearCodexUpstreamError(c)
	attempt.Record(d) // Delayed response from the failed attempt must not contaminate the successful retry.
	RecordConsumeLog(c, 1, RecordConsumeLogParams{ChannelId: 7, ModelName: "model"})
	var logs []*Log
	require.NoError(t, LOG_DB.Order("id asc").Find(&logs).Error)
	require.Len(t, logs, 3)
	for _, log := range logs[:2] {
		assert.Equal(t, d.Message, gjson.Get(log.Other, "admin_info.upstream_error.message").String())
		assert.NotContains(t, log.Content, d.Message)
	}
	assert.False(t, gjson.Get(logs[2].Other, "admin_info.upstream_error").Exists())
	formatUserLogs(logs, 0)
	for _, log := range logs {
		assert.NotContains(t, log.Other, d.Message)
		assert.NotContains(t, log.Other, "upstream_error")
	}
}
