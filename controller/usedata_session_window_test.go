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

func TestGetFullUserSessionWindowsGroupsTargetsByUser(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	user := &model.User{Id: 912345, Username: "session-full-user", DisplayName: "窗口用户", Password: "test-password", Group: "default"}
	require.NoError(t, db.Create(user).Error)

	fullHeader := http.Header{}
	fullHeader.Set("X-Codex2API-Session-Limit", "3")
	fullHeader.Set("X-Codex2API-Session-Used", "3")
	fullHeader.Set("X-Codex2API-Session-Window-Seconds", "4800")
	model.UpdateUserSessionWindowFromHeader(user.Id, "https://codex-full.example", fullHeader)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/data/session-windows/full", nil)
	GetFullUserSessionWindows(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			FullUserCount   int                         `json:"full_user_count"`
			FullTargetCount int                         `json:"full_target_count"`
			Items           []fullUserSessionWindowItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.GreaterOrEqual(t, payload.Data.FullUserCount, 1)
	require.GreaterOrEqual(t, payload.Data.FullTargetCount, 1)
	var found *fullUserSessionWindowItem
	for index := range payload.Data.Items {
		if payload.Data.Items[index].UserID == user.Id {
			found = &payload.Data.Items[index]
			break
		}
	}
	require.NotNil(t, found)
	require.Equal(t, user.Username, found.Username)
	require.Equal(t, user.DisplayName, found.DisplayName)
	require.Len(t, found.Targets, 1)
	require.Equal(t, "https://codex-full.example", found.Targets[0].Target)
}
