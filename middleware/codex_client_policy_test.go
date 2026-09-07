package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
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

func TestDistributorRejectsGPT54BackgroundBeforeRootWait(test *testing.T) {
	require.NoError(test, i18n.Init())
	for _, source := range []string{"guardian_review", "subagent", "ambient_suggestions", "thread_title", "system"} {
		test.Run(source, func(test *testing.T) {
			recorder := httptest.NewRecorder()
			request, _ := gin.CreateTestContext(recorder)
			request.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"test"}`))
			request.Request.Header.Set("Content-Type", "application/json")
			request.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_source":"`+source+`","request_kind":"turn"}`)
			Distribute()(request)
			assert.Equal(test, http.StatusBadRequest, recorder.Code)
			assert.Equal(test, "codex_passive_model_restricted", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
			assert.Zero(test, common.GetContextKeyInt(request, constant.ContextKeyChannelId))
		})
	}
}

func TestGPT54RestrictionPreservesUserModelsAndOnlyBlocksPassiveGPT54(test *testing.T) {
	require.NoError(test, i18n.Init())
	for _, scenario := range []struct {
		model, source, subagent string
		blocked                 bool
	}{
		{"gpt-5.4", "user", "", false},
		{"gpt-5.4", "", "", false},
		{"gpt-5.4", "", "guardian", true},
		{"gpt-5.4-mini", "subagent", "review", false},
		{"gpt-5.6-luna", "thread_title", "", false},
		{"gpt-6-astra", "guardian_review", "guardian", false},
	} {
		request, _ := gin.CreateTestContext(httptest.NewRecorder())
		request.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		assert.Equal(test, scenario.blocked, rejectRestrictedCodexPassiveModel(request, scenario.model, relaychannel.CodexRootSessionResolution{ThreadSource: scenario.source, SubagentKind: scenario.subagent}))
	}
}

func TestDistributorRejectsGPT54PassiveMetadataInBody(test *testing.T) {
	require.NoError(test, i18n.Init())
	recorder := httptest.NewRecorder()
	request, _ := gin.CreateTestContext(recorder)
	body, err := common.Marshal(map[string]any{
		"model": "gpt-5.4", "input": "test",
		"client_metadata": map[string]string{"x-codex-turn-metadata": `{"thread_source":"guardian_review","request_kind":"turn"}`},
	})
	require.NoError(test, err)
	request.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	request.Request.Header.Set("Content-Type", "application/json")
	Distribute()(request)
	assert.Equal(test, http.StatusBadRequest, recorder.Code)
	assert.Equal(test, "codex_passive_model_restricted", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
}
