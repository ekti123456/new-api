package model

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUpstreamErrorLogsAreAdminOnlyAndAttemptScoped(t *testing.T) {
	truncateTables(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set(common.RequestIdKey, "cause-request")
	attempt := common.CodexUpstreamErrorAttemptForContext(c)
	diagnostic := common.CodexUpstreamError{RequestID: "cause-request", ChannelID: 7, Message: "connection reset by peer", Source: "transport", Stage: "ws_read"}
	attempt.Record(diagnostic)
	RecordErrorLog(c, 1, 7, "model", "", "generic error", 0, 1, true, "", nil)
	common.ClearCodexUpstreamError(c)
	attempt.Record(diagnostic)
	RecordErrorLog(c, 1, 7, "model", "", "second attempt", 0, 1, true, "", nil)
	var logs []*Log
	require.NoError(t, LOG_DB.Order("id").Find(&logs).Error)
	require.Len(t, logs, 2)
	assert.Equal(t, diagnostic.Message, gjson.Get(logs[0].Other, "admin_info.upstream_error.message").String())
	assert.False(t, gjson.Get(logs[1].Other, "admin_info.upstream_error").Exists())
	FormatAdminLogs(logs)
	assert.Contains(t, logs[0].Other, diagnostic.Message)
	formatUserLogs(logs, 0)
	for _, entry := range logs {
		assert.NotContains(t, entry.Other, "upstream_error")
		assert.NotContains(t, entry.Content, diagnostic.Message)
	}
}
