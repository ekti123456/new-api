package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDistributorRejectsOutdatedUAWithoutRoutingOrRetries(test *testing.T) {
	require.NoError(test, i18n.Init())
	setting := operation_setting.GetUserAgentRoutingSetting()
	original := *setting
	test.Cleanup(func() { *setting = original })
	*setting = operation_setting.UserAgentRoutingSetting{Enabled: false, VersionCheckEnabled: true, MinimumVersions: map[string]string{"Codex Desktop": "0.153.0"}}
	for _, uri := range []string{"/v1/responses", "/v1/responses/compact", "/v1/realtime?model=gpt-5.4"} {
		recorder := httptest.NewRecorder()
		request, _ := gin.CreateTestContext(recorder)
		method := http.MethodPost
		if strings.Contains(uri, "/realtime") {
			method = http.MethodGet
		}
		request.Request = httptest.NewRequest(method, uri, nil)
		request.Request.Header.Set("User-Agent", "Codex Desktop/0.152.0")
		common.SetContextKey(request, constant.ContextKeyUserAgentRoutingWhitelist, true)
		Distribute()(request)
		assert.Equal(test, http.StatusBadRequest, recorder.Code)
		assert.Equal(test, "client_version_unsupported", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
		assert.Contains(test, recorder.Body.String(), "0.153.0")
		assert.Contains(test, recorder.Body.String(), "0.152.0")
		assert.Empty(test, recorder.Header().Get("Retry-After"))
		assert.Zero(test, common.GetContextKeyInt(request, constant.ContextKeyChannelId))
	}
}
