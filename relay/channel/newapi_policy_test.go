package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
)

func TestApplyNewAPIPolicyHeadersSignsV1IdentityAndMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "0123456789abcdef0123456789abcdef"
	apiKey := "sk-codex2api-test"
	keyDigest := sha256.Sum256([]byte(apiKey))
	binding := newAPIPolicyBinding{
		PlatformID:          "primary-newapi",
		Target:              "http://127.0.0.1:18095",
		CodexKeyFingerprint: hex.EncodeToString(keyDigest[:]),
		Secret:              secret,
		Enabled:             true,
		Profile:             "strict",
		Mode:                "enforce",
	}
	configurePolicyTest(t, []newAPIPolicyBinding{binding})

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/responses?client=true", nil)
	context.Request.RemoteAddr = "203.0.113.9:4567"
	common2.SetContextKey(context, constant.ContextKeyUserName, "policy-user")

	body := []byte(`{"model":"gpt-5","input":"hello"}`)
	upstreamRequest, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18095/v1/responses?upstream=true", bytes.NewReader(body))
	require.NoError(t, err)
	upstreamRequest.Header.Set("X-NewAPI-Signature", "client-controlled")
	info := &relaycommon.RelayInfo{
		UserId:                  42,
		UserEmail:               "user@example.com",
		UserGroup:               "default",
		RequestId:               "req-newapi-policy-1",
		OriginModelName:         "gpt-5",
		RelayFormat:             types.RelayFormatOpenAIResponses,
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAIResponses},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         7,
			ApiKey:            apiKey,
			UpstreamModelName: "gpt-5-codex",
		},
	}

	require.NoError(t, applyNewAPIPolicyHeaders(context, upstreamRequest, info, bytes.NewReader(body)))

	bodyDigest := sha256.Sum256(body)
	bodyDigestHex := hex.EncodeToString(bodyDigest[:])
	assert.Equal(t, "42", upstreamRequest.Header.Get("X-NewAPI-User-ID"))
	assert.Equal(t, "203.0.113.9", upstreamRequest.Header.Get("X-NewAPI-Client-IP"))
	assert.Equal(t, info.RequestId, upstreamRequest.Header.Get("X-NewAPI-Request-ID"))
	assert.Equal(t, http.MethodPost, upstreamRequest.Header.Get("X-NewAPI-Method"))
	assert.Equal(t, "/v1/responses", upstreamRequest.Header.Get("X-NewAPI-Path"))
	assert.Equal(t, bodyDigestHex, upstreamRequest.Header.Get("X-NewAPI-Body-SHA256"))
	assert.Equal(t, newAPISignatureVersion, upstreamRequest.Header.Get("X-NewAPI-Signature-Version"))

	canonical := "v1\n" + upstreamRequest.Header.Get("X-NewAPI-Timestamp") + "\n" + info.RequestId + "\n42\n203.0.113.9\nPOST\n/v1/responses\n" + bodyDigestHex
	assert.Equal(t, newAPIHMAC(secret, canonical), upstreamRequest.Header.Get("X-NewAPI-Signature"))

	encodedMeta := upstreamRequest.Header.Get("X-NewAPI-Policy-Meta")
	metaJSON, err := base64.RawURLEncoding.DecodeString(encodedMeta)
	require.NoError(t, err)
	var meta newAPIPolicyMeta
	require.NoError(t, common2.Unmarshal(metaJSON, &meta))
	assert.Equal(t, binding.PlatformID, meta.PlatformID)
	assert.Equal(t, "policy-user", meta.UserName)
	assert.Equal(t, binding.Profile, meta.Profile)
	assert.Equal(t, binding.Mode, meta.Mode)
	assert.Equal(t, "/v1/responses", meta.OriginalEndpoint)
	assert.Equal(t, string(types.RelayFormatOpenAIResponses), meta.Protocol)
	assert.Equal(t, "gpt-5-codex", meta.UpstreamModel)
	metaCanonical := newAPIPolicyMetaVersion + "\n" + info.RequestId + "\n" + bodyDigestHex + "\n" + encodedMeta
	assert.Equal(t, newAPIHMAC(secret, metaCanonical), upstreamRequest.Header.Get("X-NewAPI-Policy-Meta-Signature"))

	forwardedBody, err := io.ReadAll(upstreamRequest.Body)
	require.NoError(t, err)
	assert.Equal(t, body, forwardedBody)
}

func TestApplyNewAPIPolicyHeadersHashesReaderOnlyBodyWithoutConsumingIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "abcdef0123456789abcdef0123456789"
	apiKey := "sk-reader-only"
	keyDigest := sha256.Sum256([]byte(apiKey))
	configurePolicyTest(t, []newAPIPolicyBinding{{
		PlatformID:          "newapi-reader",
		Target:              "https://guard.example/base",
		CodexKeyFingerprint: hex.EncodeToString(keyDigest[:]),
		Secret:              secret,
		Enabled:             true,
	}})

	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	storage, err := common2.CreateBodyStorage(body)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	reader := common2.ReaderOnly(storage)
	upstreamRequest, err := http.NewRequest(http.MethodPost, "https://guard.example/base/v1/chat/completions", reader)
	require.NoError(t, err)
	require.Nil(t, upstreamRequest.GetBody)

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/chat/completions", nil)
	context.Request.RemoteAddr = "198.51.100.4:2345"
	info := &relaycommon.RelayInfo{
		UserId:      8,
		RequestId:   "req-reader-only",
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: apiKey},
	}

	require.NoError(t, applyNewAPIPolicyHeaders(context, upstreamRequest, info, reader))
	digest := sha256.Sum256(body)
	assert.Equal(t, hex.EncodeToString(digest[:]), upstreamRequest.Header.Get("X-NewAPI-Body-SHA256"))
	forwardedBody, err := io.ReadAll(upstreamRequest.Body)
	require.NoError(t, err)
	assert.Equal(t, body, forwardedBody)
}

func TestApplyNewAPIPolicyHeadersStripsSpoofedHeadersOutsideBindingScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "00112233445566778899aabbccddeeff"
	apiKey := "sk-expected"
	keyDigest := sha256.Sum256([]byte(apiKey))
	configurePolicyTest(t, []newAPIPolicyBinding{{
		PlatformID:          "primary",
		Target:              "https://guard.example/v1",
		CodexKeyFingerprint: hex.EncodeToString(keyDigest[:]),
		Secret:              secret,
		Enabled:             true,
	}})

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/responses", nil)
	upstreamRequest, err := http.NewRequest(http.MethodPost, "https://guard.example/v10/responses", bytes.NewReader(nil))
	require.NoError(t, err)
	for _, name := range newAPIPolicyHeaderNames {
		upstreamRequest.Header.Set(name, "spoofed")
	}
	info := &relaycommon.RelayInfo{
		UserId:      9,
		RequestId:   "req-outside-scope",
		RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-wrong"},
	}

	require.NoError(t, applyNewAPIPolicyHeaders(context, upstreamRequest, info, bytes.NewReader(nil)))
	for _, name := range newAPIPolicyHeaderNames {
		assert.Empty(t, upstreamRequest.Header.Get(name), name)
	}
}

func TestLoadNewAPIPolicyConfigSupportsLegacyTargets(t *testing.T) {
	t.Setenv("CODEX2API_POLICY_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "")
	t.Setenv("CODEX2API_POLICY_BINDINGS", "")
	t.Setenv("CODEX2API_POLICY_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CODEX2API_POLICY_TARGETS", "http://127.0.0.1:18095, https://guard.example/base")
	t.Setenv("CODEX2API_POLICY_PLATFORM_ID", "legacy-newapi")

	cfg, err := loadNewAPIPolicyConfig()
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.Len(t, cfg.Bindings, 2)
	assert.Equal(t, "legacy-newapi", cfg.Bindings[0].PlatformID)
	assert.Equal(t, "balanced", cfg.Bindings[0].Profile)
	assert.Equal(t, "shadow", cfg.Bindings[0].Mode)
	assert.Empty(t, cfg.Bindings[0].CodexKeyFingerprint)
}

func TestPolicyTargetMatchesOriginPathAndWebSocketScheme(t *testing.T) {
	tests := []struct {
		name   string
		target string
		actual string
		match  bool
	}{
		{name: "base URL", target: "http://127.0.0.1:18095", actual: "http://127.0.0.1:18095/v1/responses", match: true},
		{name: "path boundary", target: "https://guard.example/v1", actual: "https://guard.example/v10/responses", match: false},
		{name: "default port", target: "https://guard.example", actual: "https://guard.example:443/v1/messages", match: true},
		{name: "websocket equivalent", target: "https://guard.example", actual: "wss://guard.example/v1/responses", match: true},
		{name: "different host", target: "https://guard.example", actual: "https://other.example/v1/responses", match: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := url.Parse(test.actual)
			require.NoError(t, err)
			assert.Equal(t, test.match, policyTargetMatches(test.target, actual))
		})
	}
}

func configurePolicyTest(t *testing.T, bindings []newAPIPolicyBinding) {
	t.Helper()
	raw, err := common2.Marshal(bindings)
	require.NoError(t, err)
	t.Setenv("CODEX2API_POLICY_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "true")
	t.Setenv("CODEX2API_POLICY_BINDINGS", string(raw))
	t.Setenv("CODEX2API_POLICY_SECRET", "")
	t.Setenv("CODEX2API_POLICY_TARGETS", "")
	t.Setenv("CODEX2API_POLICY_PLATFORM_ID", "")
}
