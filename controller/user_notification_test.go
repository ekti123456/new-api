package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userNotificationAPIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Items       []model.UserNotification `json:"items"`
		UnreadCount int64                    `json:"unread_count"`
	} `json:"data"`
}

func setupUserNotificationControllerTestDB(t *testing.T) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.UserNotification{}))
	require.NoError(t, db.Create(&model.User{
		Id: 10, Username: "alice", Password: "unused-password", Email: "alice@example.com", AffCode: "a010",
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Id: 20, Username: "no-email", Password: "unused-password", AffCode: "a020",
	}).Error)
}

func decodeUserNotificationAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) userNotificationAPIResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload userNotificationAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

func TestSendAndReadPersonalUserNotification(t *testing.T) {
	setupUserNotificationControllerTestDB(t)

	sendRecorder := httptest.NewRecorder()
	sendContext, _ := gin.CreateTestContext(sendRecorder)
	sendContext.Set("id", 1)
	sendContext.Request = httptest.NewRequest(http.MethodPost, "/api/data/user-performance-anomalies/contact", bytes.NewBufferString(`{
		"user_id":10,"channel":"in_app","title":"Performance alert","content":"Please check your requests"
	}`))
	sendContext.Request.Header.Set("Content-Type", "application/json")
	SendUserPerformanceAlert(sendContext)
	require.True(t, decodeUserNotificationAPIResponse(t, sendRecorder).Success)

	listRecorder := httptest.NewRecorder()
	listContext, _ := gin.CreateTestContext(listRecorder)
	listContext.Set("id", 10)
	listContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/notifications/personal", nil)
	GetPersonalNotifications(listContext)
	listPayload := decodeUserNotificationAPIResponse(t, listRecorder)
	require.True(t, listPayload.Success)
	require.Equal(t, int64(1), listPayload.Data.UnreadCount)
	require.Len(t, listPayload.Data.Items, 1)
	require.Equal(t, "Performance alert", listPayload.Data.Items[0].Title)

	readRecorder := httptest.NewRecorder()
	readContext, _ := gin.CreateTestContext(readRecorder)
	readContext.Set("id", 10)
	readContext.Request = httptest.NewRequest(http.MethodPost, "/api/user/notifications/personal/read", nil)
	MarkPersonalNotificationsRead(readContext)
	require.True(t, decodeUserNotificationAPIResponse(t, readRecorder).Success)

	_, unreadCount, err := model.ListUserNotifications(10)
	require.NoError(t, err)
	require.Zero(t, unreadCount)
}

func TestSendPerformanceEmailRejectsUserWithoutEmail(t *testing.T) {
	setupUserNotificationControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/data/user-performance-anomalies/contact", bytes.NewBufferString(`{
		"user_id":20,"channel":"email","title":"Performance alert","content":"Please check your requests"
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	SendUserPerformanceAlert(ctx)

	payload := decodeUserNotificationAPIResponse(t, recorder)
	require.False(t, payload.Success)
	require.Equal(t, "target user has no email address", payload.Message)
}
