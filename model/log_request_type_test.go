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

func TestConsumeAndErrorLogsKeepAdminRequestType(t *testing.T) {
	truncateTables(t)
	previousLog, previousExport := common.LogConsumeEnabled, common.DataExportEnabled
	t.Cleanup(func() { common.LogConsumeEnabled, common.DataExportEnabled = previousLog, previousExport })
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	common.ObserveCodexRequestClassification(c, "guardian_review", "turn", "guardian", "resolved", false)
	common.ObserveCodexRequestClassification(c, "guardian_review", "turn", "guardian", "resolved", true)
	RecordConsumeLog(c, 1, RecordConsumeLogParams{ModelName: "gpt-6-astra", Quota: 10, PromptTokens: 8, CompletionTokens: 2})
	RecordErrorLog(c, 1, 0, "gpt-6-astra", "", "status_code=503 compaction_upstream_unavailable", 0, 1, true, "", nil)
	var logs []*Log
	require.NoError(t, LOG_DB.Order("id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	for _, log := range logs {
		assert.Equal(t, "related_internal", gjson.Get(log.Other, "admin_info.request_classification.type").String())
		assert.Equal(t, "independent_internal", gjson.Get(log.Other, "admin_info.request_classification.ingress_type").String())
		assert.Equal(t, "guardian_review", gjson.Get(log.Other, "admin_info.request_classification.thread_source").String())
	}
	formatUserLogs(logs, 0)
	for _, log := range logs {
		assert.False(t, gjson.Get(log.Other, "admin_info.request_classification").Exists())
	}
}
