package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetPerfMetricErrorsReturnsPagedFinalFailures(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PerfMetricError{}))
	require.NoError(t, db.Create(&model.PerfMetricError{
		CreatedAt: 100, UserId: 7, Username: "alice", ModelName: "gpt-a", Group: "pro",
		StatusCode: 503, ErrorType: "upstream", ErrorCode: "no_channel", ErrorReason: "no available channel",
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/performance-errors?model_name=gpt-a&p=1&page_size=10", nil)
	GetPerfMetricErrors(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Total int                     `json:"total"`
			Items []model.PerfMetricError `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, 1, payload.Data.Total)
	require.Len(t, payload.Data.Items, 1)
	require.Equal(t, "gpt-a", payload.Data.Items[0].ModelName)
}
