package model

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestFormatUserLogsStripsQuotaSaturation verifies the admin-only quota
// saturation marker (nested under other.admin_info) is removed for non-admin
// log views, since formatUserLogs strips the whole admin_info object.
func TestFormatUserLogsStripsQuotaSaturation(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"model_price": 0.004,
		"admin_info": map[string]interface{}{
			"quota_saturation": map[string]interface{}{
				"op":      "QuotaFromDecimal",
				"kind":    "overflow",
				"clamped": common.MaxQuota,
			},
		},
	})
	logs := []*Log{{Other: other}}

	formatUserLogs(logs, 0)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	_, hasAdminInfo := parsed["admin_info"]
	require.False(t, hasAdminInfo, "admin_info (and nested quota_saturation) must be stripped for non-admin views")
	// Non-admin billing fields remain visible.
	require.Contains(t, parsed, "model_price")
}

func TestAppendRequestUserAgentStoresSanitizedAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx.Request.Header.Set("User-Agent", "codex-cli/1.2\r\ninjected\x00")
	other := map[string]interface{}{
		"admin_info": map[string]interface{}{"use_channel": []int{1}},
	}

	result := appendRequestUserAgent(ctx, other)

	adminInfo, ok := result["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "codex-cli/1.2injected", adminInfo["user_agent"])
	require.Equal(t, []int{1}, adminInfo["use_channel"])
}

func TestAppendRequestUserAgentLimitsUtf8Length(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx.Request.Header.Set("User-Agent", strings.Repeat("界", 200))

	result := appendRequestUserAgent(ctx, nil)

	adminInfo := result["admin_info"].(map[string]interface{})
	userAgent := adminInfo["user_agent"].(string)
	require.LessOrEqual(t, len(userAgent), maxLogUserAgentBytes)
	require.True(t, strings.HasSuffix(userAgent, "界"))
}

func TestFormatUserLogsStripsRequestUserAgent(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"request_path": "/v1/responses",
		"admin_info": map[string]interface{}{
			"user_agent": "codex-cli/1.2",
		},
	})
	logs := []*Log{{Other: other}}

	formatUserLogs(logs, 0)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	require.NotContains(t, parsed, "admin_info")
	require.Equal(t, "/v1/responses", parsed["request_path"])
}
