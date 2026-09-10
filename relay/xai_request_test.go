package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesXAICompatibilityAfterOverridesAndPassthrough(t *testing.T) {
	t.Setenv("CODEX2API_POLICY_ENABLED", "false")
	settings := model_setting.GetGlobalSettings()
	previous := settings.PassThroughRequestEnabled
	t.Cleanup(func() { settings.PassThroughRequestEnabled = previous })
	for _, test := range []struct {
		name        string
		channelType int
		passthrough bool
		global      bool
		override    bool
	}{
		{name: "xai normal", channelType: constant.ChannelTypeXai},
		{name: "xai channel passthrough", channelType: constant.ChannelTypeXai, passthrough: true},
		{name: "xai global passthrough", channelType: constant.ChannelTypeXai, global: true},
		{name: "xai after override", channelType: constant.ChannelTypeXai, override: true},
		{name: "openai unchanged", channelType: constant.ChannelTypeOpenAI},
		{name: "openai passthrough unchanged", channelType: constant.ChannelTypeOpenAI, passthrough: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings.PassThroughRequestEnabled = test.global
			const body = `{"model":"grok-4.6","input":[{"role":"user","content":"hello"}],"tools":[],"tool_choice":"auto","store":false,"stream":false,"include":["reasoning.encrypted_content"],"prompt_cache_key":"cache-1","client_metadata":{"session_id":"session-1"},"reasoning":{"effort":"max","context":"all_turns"},"text":{"verbosity":"high","format":{"type":"text"}}}`
			var captured []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				captured, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(writer, `{"error":{"message":"test capture complete","type":"invalid_request_error"}}`)
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			defer common.CleanupBodyStorage(ctx)
			request, err := helper.GetAndValidateResponsesRequest(ctx)
			require.NoError(t, err)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, test.channelType)
			common.SetContextKey(ctx, constant.ContextKeyChannelId, 1)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(ctx, constant.ContextKeyChannelKey, "test-key")
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "grok-4.6")
			common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: test.passthrough})
			common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
			if test.override {
				common.SetContextKey(ctx, constant.ContextKeyChannelParamOverride, map[string]interface{}{
					"reasoning":       map[string]interface{}{"effort": "ultra", "context": "all_turns"},
					"client_metadata": map[string]interface{}{"session_id": "override"},
				})
			}
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeResponses, RelayFormat: types.RelayFormatOpenAIResponses,
				OriginModelName: "grok-4.6", RequestURLPath: "/v1/responses", Request: request,
			}
			relayErr := ResponsesHelper(ctx, info)
			require.NotNil(t, relayErr)
			require.Equal(t, http.StatusBadRequest, relayErr.StatusCode)
			require.NotEmpty(t, captured)
			assert.Equal(t, "cache-1", gjson.GetBytes(captured, "prompt_cache_key").String())
			assert.Equal(t, "false", gjson.GetBytes(captured, "store").Raw)
			assert.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(captured, "include.0").String())
			assert.Equal(t, "text", gjson.GetBytes(captured, "text.format.type").String())
			assert.Equal(t, "session-1", gjson.GetBytes(request.ClientMetadata, "session_id").String())
			assert.Equal(t, "max", request.Reasoning.Effort)
			if test.channelType == constant.ChannelTypeXai {
				assert.False(t, gjson.GetBytes(captured, "client_metadata").Exists())
				assert.False(t, gjson.GetBytes(captured, "reasoning.context").Exists())
				assert.False(t, gjson.GetBytes(captured, "text.verbosity").Exists())
				assert.False(t, gjson.GetBytes(captured, "tool_choice").Exists())
				assert.Equal(t, "xhigh", gjson.GetBytes(captured, "reasoning.effort").String())
			} else {
				assert.Equal(t, "session-1", gjson.GetBytes(captured, "client_metadata.session_id").String())
				assert.Equal(t, "all_turns", gjson.GetBytes(captured, "reasoning.context").String())
				assert.Equal(t, "high", gjson.GetBytes(captured, "text.verbosity").String())
				assert.Equal(t, "auto", gjson.GetBytes(captured, "tool_choice").String())
				assert.Equal(t, "max", gjson.GetBytes(captured, "reasoning.effort").String())
			}
		})
	}
}
