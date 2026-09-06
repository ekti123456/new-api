package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureAlphaSearchPolicy(test *testing.T, target, apiKey string) {
	test.Helper()
	digest := sha256.Sum256([]byte(apiKey))
	bindings, err := common.Marshal([]map[string]any{{
		"platform_id": "newapi", "target": target, "enabled": true,
		"codex_key_fingerprint": hex.EncodeToString(digest[:]),
		"secret":                "0123456789abcdef0123456789abcdef",
	}})
	require.NoError(test, err)
	test.Setenv("CODEX2API_POLICY_ENABLED", "true")
	test.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "true")
	test.Setenv("CODEX2API_POLICY_BINDINGS", string(bindings))
}

func TestAlphaSearchForwardsBoundOpenAIChannel(test *testing.T) {
	const apiKey = "sk-alpha-search-test-only"
	type capturedRequest struct {
		path   string
		method string
		header http.Header
		body   []byte
		err    error
	}
	received := make(chan capturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		received <- capturedRequest{request.URL.RequestURI(), request.Method, request.Header.Clone(), body, err}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":{"message":"upstream search validation","type":"invalid_request_error","code":"search_validation"}}`)
	}))
	defer upstream.Close()
	baseURL := upstream.URL + "/codex"
	configureAlphaSearchPolicy(test, baseURL+"/v1", apiKey)
	raw := []byte(`{"model":"search-model","commands":{"search_query":[{"q":"test"}]},"future_field":{"enabled":true}}`)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search?trace=1", strings.NewReader(string(raw)))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set("X-Codex-Window-Id", "search-window:0")
	common.SetContextKey(context, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(context, constant.ContextKeyChannelId, 12)
	common.SetContextKey(context, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(context, constant.ContextKeyChannelKey, apiKey)
	common.SetContextKey(context, constant.ContextKeyOriginalModel, "search-model")
	context.Set("model_mapping", `{"search-model":"search-upstream"}`)
	info := &relaycommon.RelayInfo{
		Request:         &dto.AlphaSearchRequest{Model: "search-model", RawBody: raw},
		OriginModelName: "search-model", RequestURLPath: context.Request.URL.RequestURI(),
		RelayMode: relayconstant.RelayModeAlphaSearch, RelayFormat: types.RelayFormatOpenAI,
		UserId: 1, RequestId: "alpha-search-regression",
	}
	requestError := AlphaSearchHelper(context, info)
	require.NotNil(test, requestError)
	assert.Equal(test, http.StatusBadRequest, requestError.StatusCode)
	assert.Contains(test, requestError.Error(), "upstream search validation")
	select {
	case captured := <-received:
		require.NoError(test, captured.err)
		assert.Equal(test, "/codex/v1/alpha/search?trace=1", captured.path)
		assert.Equal(test, http.MethodPost, captured.method)
		assert.Equal(test, "Bearer "+apiKey, captured.header.Get("Authorization"))
		assert.Equal(test, "search-window:0", captured.header.Get("X-Codex-Window-Id"))
		assert.Equal(test, "1", captured.header.Get("X-NewAPI-User-ID"))
		assert.NotEmpty(test, captured.header.Get("X-NewAPI-Signature"))
		assert.JSONEq(test, `{"model":"search-upstream","commands":{"search_query":[{"q":"test"}]},"future_field":{"enabled":true}}`, string(captured.body))
	default:
		test.Fatal("alpha search never reached the configured upstream")
	}
}

func TestAlphaSearchOpenAIRequiresExactDestinationAndKeyBinding(test *testing.T) {
	const target = "https://codex.example/base"
	const apiKey = "sk-alpha-search-test-only"
	configureAlphaSearchPolicy(test, target, apiKey)
	for _, sample := range []struct {
		name        string
		channelType int
		baseURL     string
		key         string
		allowed     bool
	}{
		{"bound OpenAI", constant.ChannelTypeOpenAI, target, apiKey, true},
		{"bound subpath", constant.ChannelTypeOpenAI, target + "/api", apiKey, true},
		{"other host", constant.ChannelTypeOpenAI, "https://other.example/base", apiKey, false},
		{"path prefix collision", constant.ChannelTypeOpenAI, target + "-other", apiKey, false},
		{"different scheme", constant.ChannelTypeOpenAI, "http://codex.example/base", apiKey, false},
		{"different port", constant.ChannelTypeOpenAI, "https://codex.example:8443/base", apiKey, false},
		{"different key", constant.ChannelTypeOpenAI, target, "sk-other", false},
		{"unsupported URL semantics", constant.ChannelTypeCustom, target, apiKey, false},
		{"unrelated adaptor", constant.ChannelTypeAzure, target, apiKey, false},
		{"existing NewAPI", constant.ChannelTypeNewAPI, target, apiKey, true},
		{"existing Sub2API", constant.ChannelTypeSub2API, target, apiKey, true},
		{"existing Codex", constant.ChannelTypeCodex, target, apiKey, true},
		{"existing AdvancedCustom", constant.ChannelTypeAdvancedCustom, target, apiKey, true},
	} {
		test.Run(sample.name, func(test *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: sample.channelType, ChannelBaseUrl: sample.baseURL, ApiKey: sample.key,
			}}
			assert.Equal(test, sample.allowed, supportsAlphaSearchChannel(info))
		})
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: target, ApiKey: apiKey,
	}}
	test.Run("policy disabled", func(test *testing.T) {
		test.Setenv("CODEX2API_POLICY_ENABLED", "false")
		assert.False(test, supportsAlphaSearchChannel(info))
	})
	test.Run("binding disabled", func(test *testing.T) {
		test.Setenv("CODEX2API_POLICY_BINDINGS", `[{"platform_id":"newapi","target":"https://codex.example/base","secret":"0123456789abcdef0123456789abcdef","enabled":false}]`)
		assert.False(test, supportsAlphaSearchChannel(info))
	})
}
