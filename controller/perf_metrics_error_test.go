package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetPerfMetricErrorsReturnsPagedFinalFailures(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PerfMetricError{}))
	require.NoError(t, db.Create(&model.PerfMetricError{
		CreatedAt: time.Now().Add(-47 * time.Hour).Unix(), UserId: 7, Username: "alice", ModelName: "gpt-a", Group: "pro",
		StatusCode: 503, ErrorType: "upstream", ErrorCode: "no_channel", ErrorReason: "no available channel",
	}).Error)
	require.NoError(t, db.Create(&model.PerfMetricError{
		CreatedAt: time.Now().Add(-49 * time.Hour).Unix(), ModelName: "gpt-a", ErrorReason: "expired",
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/performance-errors?model_name=gpt-a&p=1&page_size=10&start_timestamp=1", nil)
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

func TestGetPerfMetricErrorsGroupsBeforePaginationAndExpandsFilteredSnapshot(test *testing.T) {
	db := setupModelListControllerTestDB(test)
	require.NoError(test, db.AutoMigrate(&model.PerfMetricError{}))
	now := time.Now().Unix()
	items := []model.PerfMetricError{
		{CreatedAt: now - 20, UserId: 7, Username: "alice", ModelName: "gpt-a", StatusCode: 429, ErrorType: "admission", ErrorCode: "concurrency", RequestId: "alice-first", ErrorReason: `{"message":"busy","request_id":"first"}`},
		{CreatedAt: now - 10, UserId: 8, Username: "bob", ModelName: "gpt-a", StatusCode: 429, ErrorType: "admission", ErrorCode: "concurrency", RequestId: "bob"},
		{CreatedAt: now, UserId: 7, Username: "alice", ModelName: "gpt-b", StatusCode: 429, ErrorType: "admission", ErrorCode: "concurrency", RequestId: "alice-latest", ErrorReason: `{"message":"busy","request_id":"latest"}`},
	}
	require.NoError(test, db.Create(&items).Error)
	readPage := func(query string) struct {
		Total            int64 `json:"total"`
		TotalOccurrences int64 `json:"total_occurrences"`
		Items            []struct {
			model.PerfMetricError
			OccurrenceCount int64 `json:"occurrence_count"`
		} `json:"items"`
	} {
		test.Helper()
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/performance-errors?"+query, nil)
		GetPerfMetricErrors(ctx)
		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Total            int64 `json:"total"`
				TotalOccurrences int64 `json:"total_occurrences"`
				Items            []struct {
					model.PerfMetricError
					OccurrenceCount int64 `json:"occurrence_count"`
				} `json:"items"`
			} `json:"data"`
		}
		require.Equal(test, http.StatusOK, recorder.Code)
		require.NoError(test, common.Unmarshal(recorder.Body.Bytes(), &payload))
		require.True(test, payload.Success)
		return payload.Data
	}
	grouped := readPage("grouped=true&page_size=1&p=1")
	require.Equal(test, int64(2), grouped.Total)
	require.Equal(test, int64(3), grouped.TotalOccurrences)
	require.Len(test, grouped.Items, 1)
	require.Equal(test, "alice-latest", grouped.Items[0].RequestId)
	require.Equal(test, int64(2), grouped.Items[0].OccurrenceCount)
	second := readPage("grouped=true&page_size=1&p=2")
	require.Len(test, second.Items, 1)
	require.Equal(test, "bob", second.Items[0].RequestId)
	details := readPage(fmt.Sprintf("error_group_id=%d&page_size=1&p=2", items[2].Id))
	require.Equal(test, int64(2), details.Total)
	require.Len(test, details.Items, 1)
	require.Equal(test, "alice-first", details.Items[0].RequestId)
	filtered := readPage("grouped=true&model_name=gpt-a&username=alice")
	require.Equal(test, int64(1), filtered.TotalOccurrences)
	empty := readPage(fmt.Sprintf("error_group_id=%d&username=bob", items[2].Id))
	require.Zero(test, empty.Total)
	require.Empty(test, empty.Items)
	missing := readPage("error_group_id=999999")
	require.Empty(test, missing.Items)
	var count int64
	require.NoError(test, db.Model(&model.PerfMetricError{}).Count(&count).Error)
	require.Equal(test, int64(3), count)
}

func TestGetPerfMetricErrorsRejectsInvalidGroupWithoutFallingBackToAllErrors(test *testing.T) {
	for _, groupID := range []string{"", "0", "-1", "invalid", "9223372036854775808"} {
		test.Run(groupID, func(test *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/performance-errors?error_group_id="+groupID, nil)
			GetPerfMetricErrors(ctx)
			require.Equal(test, http.StatusBadRequest, recorder.Code)
		})
	}
}
