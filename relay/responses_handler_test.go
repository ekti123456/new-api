package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesCompactPreservesClientMetadataWithoutPassingParityFields(t *testing.T) {
	globalSettings := model_setting.GetGlobalSettings()
	originalPassThrough := globalSettings.PassThroughRequestEnabled
	globalSettings.PassThroughRequestEnabled = false
	t.Cleanup(func() {
		globalSettings.PassThroughRequestEnabled = originalPassThrough
	})

	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"captured","type":"invalid_request_error"}}`)
	}))
	t.Cleanup(upstream.Close)
	apiKey := "sk-test"
	keyDigest := sha256.Sum256([]byte(apiKey))
	t.Setenv("CODEX2API_POLICY_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_BINDINGS", `[{"platform_id":"newapi","target":"`+upstream.URL+`","codex_key_fingerprint":"`+hex.EncodeToString(keyDigest[:])+`","secret":"0123456789abcdef0123456789abcdef","enabled":true}]`)
	t.Setenv("CODEX2API_POLICY_SECRET", "")
	t.Setenv("CODEX2API_POLICY_TARGETS", "")

	const (
		modelName = "gpt-5.6-sol"
		rootID    = "01a06040-0000-7000-8000-000000000001"
		turnID    = "01a06040-0000-7000-8000-000000000002"
	)
	body := `{"model":"` + modelName + `","input":[],"tools":[{"type":"function","name":"ignored"}],"reasoning":{"effort":"high"},"text":{"format":{"type":"text"}},"client_metadata":{"session_id":"` + rootID + `","thread_id":"` + rootID + `","x-codex-turn-metadata":"{\"session_id\":\"` + rootID + `\",\"thread_id\":\"` + rootID + `\",\"turn_id\":\"` + turnID + `\"}"}}`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = "203.0.113.9:4567"
	t.Cleanup(func() { common2.CleanupBodyStorage(c) })

	request, err := helper.GetAndValidateResponsesCompactionRequest(c)
	require.NoError(t, err)
	require.JSONEq(t, `{"session_id":"`+rootID+`","thread_id":"`+rootID+`","x-codex-turn-metadata":"{\"session_id\":\"`+rootID+`\",\"thread_id\":\"`+rootID+`\",\"turn_id\":\"`+turnID+`\"}"}`, string(request.ClientMetadata))

	common2.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common2.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common2.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common2.SetContextKey(c, constant.ContextKeyChannelKey, apiKey)
	common2.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
	common2.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	common2.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		RelayFormat:     types.RelayFormatOpenAIResponsesCompaction,
		UserId:          42,
		UserGroup:       "pro",
		RequestId:       "req-compact-client-metadata",
		OriginModelName: modelName,
		RequestURLPath:  "/v1/responses/compact",
		Request:         request,
	}
	relayErr := ResponsesHelper(c, info)
	require.NotNil(t, relayErr, "the test upstream intentionally rejects after capturing the request")
	require.Equal(t, http.StatusBadRequest, relayErr.StatusCode)
	require.NotEmpty(t, upstreamBody)
	require.Equal(t, rootID, gjson.GetBytes(upstreamBody, "client_metadata.session_id").String())
	require.Equal(t, turnID, gjson.Get(gjson.GetBytes(upstreamBody, "client_metadata.x-codex-turn-metadata").String(), "turn_id").String())
	require.False(t, gjson.GetBytes(upstreamBody, "tools").Exists())
	require.False(t, gjson.GetBytes(upstreamBody, "reasoning").Exists())
	require.False(t, gjson.GetBytes(upstreamBody, "text").Exists())
}

func TestResponsesCompactDropsClientMetadataForNonCodexDestination(t *testing.T) {
	t.Setenv("CODEX2API_POLICY_ENABLED", "false")
	globalSettings := model_setting.GetGlobalSettings()
	originalPassThrough := globalSettings.PassThroughRequestEnabled
	globalSettings.PassThroughRequestEnabled = false
	t.Cleanup(func() { globalSettings.PassThroughRequestEnabled = originalPassThrough })

	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"captured","type":"invalid_request_error"}}`)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-5.6-sol","input":[]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common2.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common2.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common2.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common2.SetContextKey(c, constant.ContextKeyChannelKey, "sk-direct-openai")
	common2.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-5.6-sol")
	common2.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	common2.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponsesCompact, RelayFormat: types.RelayFormatOpenAIResponsesCompaction,
		OriginModelName: "gpt-5.6-sol", RequestURLPath: "/v1/responses/compact",
		Request: &dto.OpenAIResponsesCompactionRequest{
			Model: "gpt-5.6-sol", Input: []byte(`[]`),
			ClientMetadata: []byte(`{"session_id":"01a06040-0000-7000-8000-000000000011"}`),
		},
	}
	relayErr := ResponsesHelper(c, info)
	require.NotNil(t, relayErr)
	require.NotEmpty(t, upstreamBody)
	require.False(t, gjson.GetBytes(upstreamBody, "client_metadata").Exists())
}
