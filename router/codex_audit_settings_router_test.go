package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexAuditSettingsRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetApiRouter(router)
	for _, tc := range []struct{ method, path string }{{"GET", ""}, {"PUT", ""}, {"POST", "/test"}, {"GET", "/status"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/option/codex2api-policy"+tc.path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
}
