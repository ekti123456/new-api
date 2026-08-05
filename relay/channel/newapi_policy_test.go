package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	context.Request.Header.Set("Session-Id", "conversation-42")
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
	assert.Equal(t, newAPIPolicySessionFingerprint(secret, binding.PlatformID, "42", "conversation-42"), meta.SessionFingerprint)
	assert.Len(t, meta.SessionFingerprint, 32)
	metaCanonical := newAPIPolicyMetaVersion + "\n" + info.RequestId + "\n" + bodyDigestHex + "\n" + encodedMeta
	assert.Equal(t, newAPIHMAC(secret, metaCanonical), upstreamRequest.Header.Get("X-NewAPI-Policy-Meta-Signature"))

	forwardedBody, err := io.ReadAll(upstreamRequest.Body)
	require.NoError(t, err)
	assert.Equal(t, body, forwardedBody)
}

func TestNewAPIPolicyStableSessionFingerprintUsesExplicitConversationIdentity(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		PromptCacheKey:     []byte(`"body-session"`),
		PreviousResponseID: "resp_changes_each_turn",
	}}

	if got := newAPIPolicyStableSessionID(context, info); got != "body-session" {
		t.Fatalf("stable session id = %q, want body-session", got)
	}
	context.Request.Header.Set("Conversation-Id", "header-session")
	if got := newAPIPolicyStableSessionID(context, info); got != "header-session" {
		t.Fatalf("header session id = %q, want header-session", got)
	}

	first := newAPIPolicySessionFingerprint("0123456789abcdef0123456789abcdef", "newapi", "42", "header-session")
	repeat := newAPIPolicySessionFingerprint("0123456789abcdef0123456789abcdef", "newapi", "42", "header-session")
	otherUser := newAPIPolicySessionFingerprint("0123456789abcdef0123456789abcdef", "newapi", "43", "header-session")
	if len(first) != 32 || first != repeat || first == otherUser {
		t.Fatalf("unexpected scoped fingerprint first=%q repeat=%q other_user=%q", first, repeat, otherUser)
	}
}

func TestNewAPIPolicyStableSessionIDDoesNotUsePreviousResponseID(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{PreviousResponseID: "resp_123"}}
	if got := newAPIPolicyStableSessionID(context, info); got != "" {
		t.Fatalf("previous_response_id became a session identity: %q", got)
	}
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
	context.Set(newAPIPolicyRequestContextGinKey, newAPIPolicyRequestContext{Secret: "stale-secret"})
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
	stale, _ := context.Get(newAPIPolicyRequestContextGinKey)
	assert.Nil(t, stale)
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

func TestVerifyNewAPIPolicyDecisionRejectsTamperingAndRequestMismatch(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	requestContext := newAPIPolicyRequestContext{RequestID: "req-policy-response", Secret: secret}
	header := signedPolicyDecisionHeader(secret, requestContext.RequestID)

	decision, err := verifyNewAPIPolicyDecision(header, requestContext)
	require.NoError(t, err)
	assert.Equal(t, "dec_0123456789abcdef", decision.DecisionID)
	assert.True(t, decision.StrikeEligible)

	tampered := header.Clone()
	tampered.Set("X-Codex2API-Policy-Severity", "critical")
	_, err = verifyNewAPIPolicyDecision(tampered, requestContext)
	require.ErrorContains(t, err, "signature mismatch")

	mismatched := requestContext
	mismatched.RequestID = "req-other"
	_, err = verifyNewAPIPolicyDecision(header, mismatched)
	require.ErrorContains(t, err, "request id")
}

func TestVerifiedNewAPIPolicyDecisionSkipsChannelRetry(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	requestContext := newAPIPolicyRequestContext{
		RequestID: "req-policy-response", Secret: secret,
		Enforcement: newAPIPolicyEnforcementConfig{BanAfter: 2, WindowSeconds: 86400},
	}
	request, err := http.NewRequest(http.MethodPost, "http://codex2api.example/v1/responses", nil)
	require.NoError(t, err)
	request = request.WithContext(context.WithValue(request.Context(), newAPIPolicyRequestContextKey{}, requestContext))
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     signedPolicyDecisionHeader(secret, requestContext.RequestID),
		Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"blocked","type":"invalid_request_error","code":"request_policy_violation"}}`)),
		Request:    request,
	}
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.True(t, processNewAPIPolicyResponse(ginContext, response))
	newAPIError := service.RelayErrorHandler(ginContext, response, false)
	assert.True(t, types.IsSkipRetryError(newAPIError))
	assert.False(t, types.IsRecordErrorLog(newAPIError))
}

func TestProcessNewAPIPolicyResponseVerifiesSignedDecisionFromFragmentedSSE(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	requestContext := newAPIPolicyRequestContext{
		RequestID: "req-policy-response", Secret: secret,
		Enforcement: newAPIPolicyEnforcementConfig{BanAfter: 2, WindowSeconds: 86400},
	}
	header := signedPolicyDecisionHeader(secret, requestContext.RequestID)
	policyDetails := map[string]any{
		"request_id": requestContext.RequestID, "decision_id": header.Get("X-Codex2API-Policy-Decision-ID"),
		"action": header.Get("X-Codex2API-Policy-Action"), "profile": header.Get("X-Codex2API-Policy-Profile"),
		"reason_code": header.Get("X-Codex2API-Policy-Reason"), "severity": header.Get("X-Codex2API-Policy-Severity"),
		"strike_eligible": true, "rule_version": header.Get("X-Codex2API-Policy-Rule-Version"),
		"evidence_sha256":   header.Get("X-Codex2API-Policy-Evidence-SHA256"),
		"signature_version": "v1", "response_signature": header.Get("X-Codex2API-Policy-Response-Signature"),
	}
	event, err := common2.Marshal(map[string]any{
		"type": "response.failed",
		"response": map[string]any{"error": map[string]any{
			"code": "cyber_policy", "details": map[string]any{"codex2api_policy": policyDetails},
		}},
	})
	require.NoError(t, err)
	stream := "data: " + string(event) + "\n\ndata: [DONE]\n\n"

	request, err := http.NewRequest(http.MethodPost, "http://codex2api.example/v1/responses", nil)
	require.NoError(t, err)
	request = request.WithContext(context.WithValue(request.Context(), newAPIPolicyRequestContextKey{}, requestContext))
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(iotest.OneByteReader(strings.NewReader(stream))),
		Request:    request,
	}
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())

	assert.False(t, processNewAPIPolicyResponse(ginContext, response))
	streamBody, ok := response.Body.(*newAPIPolicyStreamBody)
	require.True(t, ok)
	forwarded, err := io.ReadAll(streamBody)
	require.NoError(t, err)
	assert.Equal(t, stream, string(forwarded))
	_, verified := streamBody.seen[header.Get("X-Codex2API-Policy-Decision-ID")]
	assert.True(t, verified)
}

func TestLoadNewAPIPolicyEnforcementDefaultsAreSafe(t *testing.T) {
	for _, name := range []string{
		"CODEX2API_POLICY_AUDIT_ENABLED", "CODEX2API_POLICY_STRIKE_ENABLED",
		"CODEX2API_POLICY_ACCOUNT_BAN_ENABLED", "CODEX2API_POLICY_IP_BLOCK_ENABLED",
		"CODEX2API_POLICY_BAN_AFTER", "CODEX2API_POLICY_WINDOW_SECONDS",
	} {
		t.Setenv(name, "")
	}

	config, err := loadNewAPIPolicyEnforcementConfig()
	require.NoError(t, err)
	assert.True(t, config.AuditEnabled)
	assert.False(t, config.StrikeEnabled)
	assert.False(t, config.AccountBanEnabled)
	assert.False(t, config.IPBlockEnabled)
	assert.Equal(t, 2, config.BanAfter)
	assert.Equal(t, 7*24*60*60, config.WindowSeconds)

	t.Setenv("CODEX2API_POLICY_ACCOUNT_BAN_ENABLED", "true")
	_, err = loadNewAPIPolicyEnforcementConfig()
	require.ErrorContains(t, err, "STRIKE_ENABLED")
}

func TestProcessNewAPIPolicyWebSocketMessageVerifiesPerTurnSignature(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	requestContext := newAPIPolicyRequestContext{
		RequestID: "req-policy-response", Secret: secret,
		Enforcement: newAPIPolicyEnforcementConfig{BanAfter: 2, WindowSeconds: 86400},
	}
	header := signedPolicyDecisionHeader(secret, requestContext.RequestID)
	eventID := "responses:7"
	eventCanonical := strings.Join([]string{
		"policy-event-v1", requestContext.RequestID, header.Get("X-Codex2API-Policy-Decision-ID"), eventID,
		header.Get("X-Codex2API-Policy-Action"), header.Get("X-Codex2API-Policy-Profile"),
		header.Get("X-Codex2API-Policy-Reason"), header.Get("X-Codex2API-Policy-Severity"),
		header.Get("X-Codex2API-Policy-Strike-Eligible"), header.Get("X-Codex2API-Policy-Rule-Version"),
		header.Get("X-Codex2API-Policy-Evidence-SHA256"),
	}, "\n")
	details := map[string]any{
		"request_id": requestContext.RequestID, "decision_id": header.Get("X-Codex2API-Policy-Decision-ID"),
		"event_id": eventID, "action": "block", "profile": "strict",
		"reason_code": "direct_target_intrusion_request", "severity": "high", "strike_eligible": true,
		"rule_version":      header.Get("X-Codex2API-Policy-Rule-Version"),
		"evidence_sha256":   header.Get("X-Codex2API-Policy-Evidence-SHA256"),
		"signature_version": "v1", "response_signature": header.Get("X-Codex2API-Policy-Response-Signature"),
		"event_signature_version": "v1", "event_signature": newAPIHMAC(secret, eventCanonical),
	}
	payload, err := common2.Marshal(map[string]any{
		"type": "error", "error": map[string]any{"code": "request_policy_violation", "details": details},
	})
	require.NoError(t, err)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Set(newAPIPolicyRequestContextGinKey, requestContext)

	result := ProcessNewAPIPolicyWebSocketMessage(ginContext, payload)
	assert.True(t, result.Verified)
	assert.False(t, result.Terminate)

	details["event_signature"] = strings.Repeat("0", 64)
	tampered, err := common2.Marshal(map[string]any{
		"type": "error", "error": map[string]any{"code": "request_policy_violation", "details": details},
	})
	require.NoError(t, err)
	assert.False(t, ProcessNewAPIPolicyWebSocketMessage(ginContext, tampered).Verified)
}

func signedPolicyDecisionHeader(secret string, requestID string) http.Header {
	header := make(http.Header)
	header.Set("X-Codex2API-Policy-Violation", "true")
	header.Set("X-Codex2API-Policy-Request-ID", requestID)
	header.Set("X-Codex2API-Policy-Decision-ID", "dec_0123456789abcdef")
	header.Set("X-Codex2API-Policy-Action", "block")
	header.Set("X-Codex2API-Policy-Profile", "strict")
	header.Set("X-Codex2API-Policy-Reason", "direct_target_intrusion_request")
	header.Set("X-Codex2API-Policy-Severity", "high")
	header.Set("X-Codex2API-Policy-Strike-Eligible", "true")
	header.Set("X-Codex2API-Policy-Rule-Version", "0123456789abcdef")
	header.Set("X-Codex2API-Policy-Evidence-SHA256", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	header.Set("X-Codex2API-Policy-Signature-Version", newAPIPolicyDecisionSignatureVersion)
	canonical := strings.Join([]string{
		"policy-decision-v1", requestID, "dec_0123456789abcdef", "block", "strict",
		"direct_target_intrusion_request", "high", "true", "0123456789abcdef",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, "\n")
	header.Set("X-Codex2API-Policy-Response-Signature", newAPIHMAC(secret, canonical))
	return header
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
