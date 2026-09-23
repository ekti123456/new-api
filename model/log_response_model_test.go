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

func TestUpstreamResponseModelSettingControlsConsumeAndErrorLogs(t *testing.T) {
	truncateTables(t)
	previous := common.UpstreamResponseModelLogEnabled.Load()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	previousLog, previousExport := common.LogConsumeEnabled, common.DataExportEnabled
	t.Cleanup(func() {
		common.UpstreamResponseModelLogEnabled.Store(previous)
		common.OptionMap = previousOptions
		common.LogConsumeEnabled, common.DataExportEnabled = previousLog, previousExport
	})
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for _, enabled := range []bool{false, true} {
		require.NoError(t, updateOptionMap("UpstreamResponseModelLogEnabled", common.Interface2String(enabled)))
		assert.Equal(t, enabled, common.UpstreamResponseModelLogEnabled.Load())
		common.BeginUpstreamResponseModel(c).Observe([]byte(`{"model":"gpt-5.6-luna"}`), "")
		RecordConsumeLog(c, 1, RecordConsumeLogParams{ModelName: "public-model", Quota: 10, PromptTokens: 8, CompletionTokens: 2, Other: map[string]interface{}{"upstream_model_name": "mapped-model"}})
		RecordErrorLog(c, 1, 0, "public-model", "", "test failure", 0, 1, true, "", nil)
	}
	var logs []Log
	require.NoError(t, LOG_DB.Order("id asc").Find(&logs).Error)
	require.Len(t, logs, 4)
	for i, log := range logs {
		assert.Equal(t, "public-model", log.ModelName)
		if i < 2 {
			assert.False(t, gjson.Get(log.Other, "upstream_response_model").Exists())
		} else {
			assert.Equal(t, "gpt-5.6-luna", gjson.Get(log.Other, "upstream_response_model").String())
		}
		if log.Type == LogTypeConsume {
			assert.Equal(t, 10, log.Quota)
			assert.Equal(t, 8, log.PromptTokens)
			assert.Equal(t, "mapped-model", gjson.Get(log.Other, "upstream_model_name").String())
		}
	}
}
