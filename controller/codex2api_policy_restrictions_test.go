package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyRestrictionAdminEndpointsRequireRootAndReleasePersistently(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CodexPolicyIPBlock{}, &model.CodexPolicyDecision{}, &model.CodexPolicyRestrictionEvent{}))
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{common.CodexPolicyOption: `{"enabled":true,"identity_forward_enabled":true,"ip_block_enabled":true,"strike_enabled":true,"ban_after":2,"window_seconds":604800,"bindings":[]}`}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
	rootKey, userKey := "restriction-root", "restriction-user"
	root := model.User{Username: "policy-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, AccessToken: &rootKey, AffCode: "policy-root"}
	user := model.User{Username: "policy-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AccessToken: &userKey, AffCode: "policy-user"}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	block := model.CodexPolicyIPBlock{IP: "203.0.113.42", UserID: user.Id, DecisionID: "endpoint-release", CreatedAt: now, ExpiresAt: now + 3600}
	require.NoError(t, db.Create(&block).Error)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	routes := router.Group("/policy", middleware.RootAuth())
	routes.GET("/ips", ListCodex2APIPolicyIPBlocks)
	routes.GET("/details", GetCodex2APIPolicyRestrictions)
	routes.POST("/release", ReleaseCodex2APIPolicyRestrictions)
	for _, key := range []string{"", userKey, rootKey} {
		request := httptest.NewRequest(http.MethodGet, "/policy/ips", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		request.RemoteAddr = block.IP + ":1234"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if key != rootKey {
			assert.Contains(t, []int{401, 403}, recorder.Code)
			continue
		}
		require.Equal(t, 200, recorder.Code, recorder.Body.String())
		assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		var page struct {
			Data struct {
				Items []model.CodexPolicyIPBlockView `json:"items"`
				Total int64                          `json:"total"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &page))
		require.Len(t, page.Data.Items, 1)
		assert.Equal(t, int64(1), page.Data.Total)
	}
	request := httptest.NewRequest(http.MethodGet, "/policy/details?ip=203.0.113.42", nil)
	request.Header.Set("Authorization", "Bearer "+rootKey)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"login_limit"`)
	assert.Contains(t, recorder.Body.String(), `"policy_disabled":false`)
	raw, err := common.Marshal(model.CodexPolicyRelease{UserID: user.Id, IPs: []model.CodexPolicyIPRelease{{IP: block.IP, DecisionID: block.DecisionID}}, Reason: "reviewed"})
	require.NoError(t, err)
	for index, expected := range []int{200, 409} {
		request = httptest.NewRequest(http.MethodPost, "/policy/release", strings.NewReader(string(raw)))
		request.Header.Set("Authorization", "Bearer "+rootKey)
		recorder = httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, expected, recorder.Code, "attempt %d: %s", index, recorder.Body.String())
	}
	// Reload the row rather than relying on an in-process allow-list/cache.
	var stored model.CodexPolicyIPBlock
	require.NoError(t, db.Where("ip = ?", block.IP).First(&stored).Error)
	assert.Positive(t, stored.RevokedAt)
	assert.Equal(t, root.Id, stored.RevokedBy)
	assert.Equal(t, "reviewed", stored.RevokeReason)
	blocked, err := model.IsCodexPolicyIPBlocked(block.IP, time.Now())
	require.NoError(t, err)
	assert.False(t, blocked)
}
