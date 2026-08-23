package model

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordConsumeLogUsesGlobalRequestIPSetting(t *testing.T) {
	truncateTables(t)
	previousEnabled := common.RequestIPLogEnabled
	t.Cleanup(func() { common.RequestIPLogEnabled = previousEnabled })

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	context.Request.RemoteAddr = "203.0.113.9:12345"
	context.Set("username", "ip-test-user")

	common.RequestIPLogEnabled = true
	RecordConsumeLog(context, 1001, RecordConsumeLogParams{ModelName: "test-model"})

	common.RequestIPLogEnabled = false
	RecordConsumeLog(context, 1001, RecordConsumeLogParams{ModelName: "test-model"})

	var logs []Log
	require.NoError(t, LOG_DB.Order("id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	assert.Equal(t, "203.0.113.9", logs[0].Ip)
	assert.Empty(t, logs[1].Ip)
}
