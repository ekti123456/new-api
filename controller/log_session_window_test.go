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

func TestGetLogsSelfStatIncludesUserSessionWindow(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	const userID = 912346
	header := http.Header{}
	header.Set("X-Codex2API-Session-Limit", "5")
	header.Set("X-Codex2API-Session-Used", "1")
	header.Set("X-Codex2API-Session-Window-Seconds", "4800")
	model.UpdateUserSessionWindowFromHeader(userID, "https://codex.example", header)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/self/stat", nil)
	c.Set("id", userID)
	c.Set("username", "session-window-user")
	GetLogsSelfStat(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			SessionWindowUsed  int `json:"session_window_used"`
			SessionWindowLimit int `json:"session_window_limit"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, 1, payload.Data.SessionWindowUsed)
	require.Equal(t, 5, payload.Data.SessionWindowLimit)
}
