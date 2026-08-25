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
	assert.Equal(t, 1, meta.RootSessionVersion)
	assert.Equal(t, newAPIPolicyRootSessionResolved, meta.RootSessionState)
	assert.Equal(t, newAPIPolicyRootSessionRelationRoot, meta.RootSessionRelation)
	assert.Equal(t, newAPIPolicyRootSessionFingerprint(binding.PlatformID, "42", "conversation-42"), meta.RootSessionFingerprint)
	assert.Len(t, meta.RootSessionFingerprint, 32)
	metaCanonical := newAPIPolicyMetaVersion + "\n" + info.RequestId + "\n" + bodyDigestHex + "\n" + encodedMeta
	assert.Equal(t, newAPIHMAC(secret, metaCanonical), upstreamRequest.Header.Get("X-NewAPI-Policy-Meta-Signature"))

	forwardedBody, err := io.ReadAll(upstreamRequest.Body)
	require.NoError(t, err)
	assert.Equal(t, body, forwardedBody)
}

func TestApplyNewAPIPolicyHeadersSeparatesGuardianLeafFromRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		secret  = "0123456789abcdef0123456789abcdef"
		rootID  = "019c8f5d-5729-7ec2-879d-da6c4f7b2301"
		childID = "019c8f5e-6b19-7bc3-bfd1-a11616b4d514"
	)
	apiKey := "sk-codex2api-guardian"
	keyDigest := sha256.Sum256([]byte(apiKey))
	binding := newAPIPolicyBinding{
		PlatformID:          "primary-newapi",
		Target:              "http://127.0.0.1:18095",
		CodexKeyFingerprint: hex.EncodeToString(keyDigest[:]),
		Secret:              secret,
		Enabled:             true,
	}
	configurePolicyTest(t, []newAPIPolicyBinding{binding})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/responses", nil)
	c.Request.RemoteAddr = "203.0.113.9:4567"
	c.Request.Header.Set("Conversation-Id", childID)
	c.Request.Header.Set("Session-Id", rootID)
	c.Request.Header.Set("Thread-Id", childID)
	c.Request.Header.Set("X-Client-Request-Id", childID)
	c.Request.Header.Set("X-Codex-Window-Id", childID+":8")
	c.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)
	c.Request.Header.Set("X-OpenAI-Subagent", "guardian")
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+childID+`","parent_thread_id":"`+rootID+`","thread_source":"future_new_source","request_kind":"background_turn","subagent_kind":"guardian"}`)

	body := []byte(`{"model":"gpt-5.6-luna","input":"review"}`)
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18095/v1/responses", bytes.NewReader(body))
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		UserId: 42, UserGroup: "default", RequestId: "req-guardian-root",
		OriginModelName: "gpt-5.6-luna", RelayFormat: types.RelayFormatOpenAIResponses,
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta:             &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: apiKey, UpstreamModelName: "gpt-5.6-luna"},
		Request:                 &dto.OpenAIResponsesRequest{ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + childID + `","x-codex-window-id":"` + childID + `:8","x-codex-parent-thread-id":"` + rootID + `","x-openai-subagent":"guardian"}`)},
	}

	require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
	encoded := req.Header.Get("X-NewAPI-Policy-Meta")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var meta newAPIPolicyMeta
	require.NoError(t, common2.Unmarshal(payload, &meta))
	require.Equal(t, newAPIPolicyRootSessionResolved, meta.RootSessionState)
	require.Equal(t, newAPIPolicyRootSessionRelationRelated, meta.RootSessionRelation)
	require.Equal(t, "future_new_source", meta.ThreadSource)
	require.Equal(t, "background_turn", meta.RequestKind)
	require.Equal(t, "guardian", meta.SubagentKind)
	require.Equal(t, newAPIPolicySessionFingerprint(secret, binding.PlatformID, "42", childID), meta.SessionFingerprint)
	require.Equal(t, newAPIPolicyRootSessionFingerprint(binding.PlatformID, "42", rootID), meta.RootSessionFingerprint)
	require.NotEqual(t, meta.SessionFingerprint, meta.RootSessionFingerprint)
}

func TestApplyNewAPIPolicyHeadersSignsRelatedMetadataWithoutSessionHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		secret  = "0123456789abcdef0123456789abcdef"
		rootID  = "019c8f5d-5729-7ec2-879d-da6c4f7b2301"
		childID = "019c8f5e-6b19-7bc3-bfd1-a11616b4d514"
	)
	apiKey := "sk-codex2api-metadata-only"
	keyDigest := sha256.Sum256([]byte(apiKey))
	binding := newAPIPolicyBinding{
		PlatformID:          "primary-newapi",
		Target:              "http://127.0.0.1:18095",
		CodexKeyFingerprint: hex.EncodeToString(keyDigest[:]),
		Secret:              secret,
		Enabled:             true,
	}
	configurePolicyTest(t, []newAPIPolicyBinding{binding})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/responses", nil)
	c.Request.RemoteAddr = "203.0.113.9:4567"
	body := []byte(`{"model":"gpt-5.6-terra","input":"background"}`)
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18095/v1/responses", bytes.NewReader(body))
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		UserId: 42, UserGroup: "default", RequestId: "req-metadata-only-root",
		OriginModelName: "gpt-5.6-terra", RelayFormat: types.RelayFormatOpenAIResponses,
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta:             &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: apiKey, UpstreamModelName: "gpt-5.6-terra"},
		Request:                 &dto.OpenAIResponsesRequest{ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + childID + `","window_id":"` + childID + `:2","forked_from_thread_id":"` + rootID + `","thread_source":"memory_consolidation","request_kind":"compact","subagent_kind":"summarizer"}`)},
	}

	require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
	encoded := req.Header.Get("X-NewAPI-Policy-Meta")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var meta newAPIPolicyMeta
	require.NoError(t, common2.Unmarshal(payload, &meta))
	require.Equal(t, newAPIPolicyRootSessionResolved, meta.RootSessionState)
	require.Equal(t, newAPIPolicyRootSessionRelationRelated, meta.RootSessionRelation)
	require.Equal(t, newAPIPolicyRootSessionFingerprint(binding.PlatformID, "42", rootID), meta.RootSessionFingerprint)
	require.Equal(t, "memory_consolidation", meta.ThreadSource)
	require.Equal(t, "compact", meta.RequestKind)
	require.Equal(t, "summarizer", meta.SubagentKind)
}

func TestAnalyzeNewAPIPolicyRootSessionUsesForkedLineageForUnknownSource(t *testing.T) {
	const (
		rootID  = "019c8f5d-5729-7ec2-879d-da6c4f7b2301"
		childID = "019c8f5e-6b19-7bc3-bfd1-a11616b4d514"
	)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+childID+`","window_id":"`+childID+`:2","forked_from_thread_id":"`+rootID+`","thread_source":"future_new_source","request_kind":"future_task"}`)

	resolution := analyzeNewAPIPolicyRootSession(c, nil, childID)
	require.Equal(t, newAPIPolicyRootSessionResolved, resolution.state)
	require.Equal(t, rootID, resolution.rootID)
	require.Equal(t, newAPIPolicyRootSessionRelationRelated, resolution.relation)
	require.Equal(t, "future_new_source", resolution.threadSource)
	require.Equal(t, "future_task", resolution.requestKind)
}

func TestResolveNewAPIPolicyRootSessionIDReportsConflictAndUnavailable(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"root-a","thread_id":"leaf-a"}`)
	if root, state := resolveNewAPIPolicyRootSessionID(context, nil, ""); root != "" || state != newAPIPolicyRootSessionConflict {
		t.Fatalf("uncorroborated child state = root %q state %q", root, state)
	}

	empty, _ := gin.CreateTestContext(httptest.NewRecorder())
	empty.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if root, state := resolveNewAPIPolicyRootSessionID(empty, nil, ""); root != "" || state != newAPIPolicyRootSessionUnavailable {
		t.Fatalf("empty identity state = root %q state %q", root, state)
	}
}

func TestResolveNewAPIPolicyRootSessionIDDefersWeakWebSocketIdentityToFrame(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
	}{
		{name: "lone Session-Id", header: "Session-Id"},
		{name: "legacy conversation", header: "Conversation-Id"},
		{name: "generic explicit session", header: "X-Session-ID"},
		{name: "OpenAI explicit session", header: "OpenAI-Session-ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			context.Request.Header.Set("Connection", "Upgrade")
			context.Request.Header.Set("Upgrade", "websocket")
			context.Request.Header.Set(test.header, "weak-handshake-session")

			legacy := newAPIPolicyStableSessionID(context, nil)
			root, state := resolveNewAPIPolicyRootSessionID(context, nil, legacy)
			require.Empty(t, root)
			require.Equal(t, newAPIPolicyRootSessionUnavailable, state)
		})
	}
}

func TestResolveNewAPIPolicyRootSessionIDKeepsCompleteWebSocketGraph(t *testing.T) {
	const (
		rootID = "01a031a2-043b-7f42-afa6-ce5491d9be64"
		leafID = "01a031a2-ca1e-7063-8ba7-f140c182c629"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	context.Request.Header.Set("Connection", "Upgrade")
	context.Request.Header.Set("Upgrade", "websocket")
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("Thread-Id", leafID)
	context.Request.Header.Set("X-Client-Request-Id", leafID)
	context.Request.Header.Set("X-Codex-Window-Id", leafID+":1")
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)

	root, state := resolveNewAPIPolicyRootSessionID(context, nil, newAPIPolicyStableSessionID(context, nil))
	require.Equal(t, rootID, root)
	require.Equal(t, newAPIPolicyRootSessionResolved, state)
}

func TestResolveNewAPIPolicyRootSessionIDTreatsOptionalJSONNullAsAbsent(t *testing.T) {
	const (
		rootID = "01a031a2-043b-7f42-afa6-ce5491d9be64"
		leafID = "01a031a2-ca1e-7063-8ba7-f140c182c629"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + leafID + `","x-codex-window-id":"` + leafID + `:1","x-codex-parent-thread-id":"` + rootID + `","client_request_id":null,"subagent_kind":null,"x-codex-turn-metadata":null}`),
	}}

	root, state := resolveNewAPIPolicyRootSessionID(context, info, "")
	require.Equal(t, rootID, root)
	require.Equal(t, newAPIPolicyRootSessionResolved, state)
}

func TestResolveNewAPIPolicyRootSessionIDNullMetadataDoesNotUpgradeOpaqueSession(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", "opaque-sdk-session")
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"x-codex-turn-metadata":null,"client_metadata":null,"thread_id":null,"subagent_kind":null}`),
	}}

	root, state := resolveNewAPIPolicyRootSessionID(context, info, newAPIPolicyStableSessionID(context, info))
	require.Equal(t, "opaque-sdk-session", root)
	require.Equal(t, newAPIPolicyRootSessionResolved, state)
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

func TestNewAPIPolicyRootSessionIDCollapsesVerifiedGuardianLineage(t *testing.T) {
	const (
		rootID  = "019c8f5d-5729-7ec2-879d-da6c4f7b2301"
		childID = "019c8f5e-6b19-7bc3-bfd1-a11616b4d514"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", childID)
	context.Request.Header.Set("Thread-Id", childID)
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)
	context.Request.Header.Set("X-OpenAI-Subagent", "guardian")
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+childID+`","parent_thread_id":"`+rootID+`","subagent_kind":"guardian"}`)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + childID + `","x-openai-subagent":"guardian","x-codex-turn-metadata":"{\"session_id\":\"` + rootID + `\",\"thread_id\":\"` + childID + `\",\"parent_thread_id\":\"` + rootID + `\",\"subagent_kind\":\"guardian\"}"}`),
	}}

	leafID := newAPIPolicyStableSessionID(context, info)
	require.Equal(t, childID, leafID)
	root, ok := newAPIPolicyRootSessionID(context, info, leafID)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDCollapsesNestedGuardianToMainTask(t *testing.T) {
	const (
		rootID         = "01a01a4b-8459-73f3-830a-c8b90a57981a"
		intermediateID = "01a031b0-8317-7b21-9297-85bbd886eb9e"
		guardianID     = "01a031b1-8205-7ce2-9284-bca567c7a9a2"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("Thread-Id", guardianID)
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", intermediateID)
	context.Request.Header.Set("X-OpenAI-Subagent", "guardian")
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+guardianID+`","parent_thread_id":"`+intermediateID+`","subagent_kind":"approval"}`)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + guardianID + `","parent_thread_id":"` + intermediateID + `","x-openai-subagent":"guardian"}`),
	}}

	leafID := newAPIPolicyStableSessionID(context, info)
	require.Equal(t, rootID, leafID)
	root, ok := newAPIPolicyRootSessionID(context, info, leafID)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
	assert.NotEqual(t, intermediateID, root)
}

func TestNewAPIPolicyRootSessionIDNeverPromotesImmediateParent(t *testing.T) {
	const (
		rootID         = "01a01a4b-8459-73f3-830a-c8b90a57981a"
		intermediateID = "01a031b0-8317-7b21-9297-85bbd886eb9e"
		guardianID     = "01a031b1-8205-7ce2-9284-bca567c7a9a2"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("Thread-Id", guardianID)
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", intermediateID)
	context.Request.Header.Set("X-OpenAI-Subagent", "guardian")

	root, ok := newAPIPolicyRootSessionID(context, nil, rootID)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
	assert.NotEqual(t, intermediateID, root)
}

func TestNewAPIPolicyRootSessionIDAcceptsMatchingExplicitAndMetadataRoot(t *testing.T) {
	const (
		rootID         = "01a01a4b-8459-73f3-830a-c8b90a57981a"
		intermediateID = "01a031b0-8317-7b21-9297-85bbd886eb9e"
		guardianID     = "01a031b1-8205-7ce2-9284-bca567c7a9a2"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("Thread-Id", guardianID)
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", intermediateID)
	// The explicit Session-Id and metadata session_id independently agree on
	// the root even when older clients omit a subagent marker.
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+guardianID+`"}`)

	root, ok := newAPIPolicyRootSessionID(context, nil, rootID)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDNativeSessionBeatsLegacyConversationPriority(t *testing.T) {
	const (
		rootID     = "01a01a4b-8459-73f3-830a-c8b90a57981a"
		guardianID = "01a031b1-8205-7ce2-9284-bca567c7a9a2"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Conversation-Id", "legacy-conversation-wins-leaf-priority")
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("Thread-Id", guardianID)
	context.Request.Header.Set("X-Client-Request-Id", guardianID)
	context.Request.Header.Set("X-Codex-Window-Id", guardianID+":7")
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)

	legacy := newAPIPolicyStableSessionID(context, nil)
	require.Equal(t, "legacy-conversation-wins-leaf-priority", legacy)
	root, ok := newAPIPolicyRootSessionID(context, nil, legacy)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDMetadataRootBeatsLegacyConversationPriority(t *testing.T) {
	const (
		rootID     = "01a01a4b-8459-73f3-830a-c8b90a57981a"
		guardianID = "01a031b1-8205-7ce2-9284-bca567c7a9a2"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Conversation-Id", "legacy-conversation")
	context.Request.Header.Set("Session-Id", guardianID)
	context.Request.Header.Set("Thread-Id", guardianID)
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)
	context.Request.Header.Set("X-OpenAI-Subagent", "guardian")
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+guardianID+`","parent_thread_id":"`+rootID+`"}`)

	legacy := newAPIPolicyStableSessionID(context, nil)
	require.Equal(t, "legacy-conversation", legacy)
	root, ok := newAPIPolicyRootSessionID(context, nil, legacy)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDReadsNestedBodyMetadata(t *testing.T) {
	const (
		rootID  = "019c8f60-b9e1-78c2-8fe2-91661a4f649c"
		childID = "019c8f61-071a-7932-934c-2baeaec1126c"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", childID)
	// The whole client_metadata value and its x-codex-turn-metadata member may
	// both arrive as JSON strings after protocol conversion.
	nested := `{"client_metadata":{"x-openai-subagent":"review","x-codex-turn-metadata":{"session_id":"` + rootID + `","thread_id":"` + childID + `","parent_thread_id":"` + rootID + `","subagent_kind":"review"}}}`
	encodedNested, err := common2.Marshal(nested)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{ClientMetadata: encodedNested}}

	root, ok := newAPIPolicyRootSessionID(context, info, childID)
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDAliasesMatchCodex2APIContract(t *testing.T) {
	const (
		rootID  = "019c8f60-b9e1-78c2-8fe2-91661a4f649c"
		childID = "019c8f61-071a-7932-934c-2baeaec1126c"
	)
	for _, test := range []struct {
		name     string
		metadata string
	}{
		{
			name:     "hyphen aliases",
			metadata: `{"session_id":"` + rootID + `","thread_id":"` + childID + `","x-client-request-id":"` + childID + `","x-codex-window-id":"` + childID + `:4","x-codex-parent-thread-id":"` + rootID + `","x-openai-subagent":"guardian"}`,
		},
		{
			name:     "underscore aliases",
			metadata: `{"session_id":"` + rootID + `","thread_id":"` + childID + `","x_client_request_id":"` + childID + `","x_codex_window_id":"` + childID + `:5","x_codex_parent_thread_id":"` + rootID + `","x_openai_subagent":"guardian"}`,
		},
		{
			name:     "underscore embedded turn metadata",
			metadata: `{"x_codex_turn_metadata":{"session_id":"` + rootID + `","thread_id":"` + childID + `","window_id":"` + childID + `:6","parent_thread_id":"` + rootID + `","subagent_kind":"guardian"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{ClientMetadata: []byte(test.metadata)}}
			root, state := resolveNewAPIPolicyRootSessionID(context, info, "")
			if state != newAPIPolicyRootSessionResolved || root != rootID {
				t.Fatalf("root=%q state=%q", root, state)
			}
		})
	}
}

func TestNewAPIPolicyRootSessionIDMetadataDepthAndEncodingBounds(t *testing.T) {
	const rootID = "019c8f64-e414-7885-9e70-f0c2911f1708"
	leaf := `{"session_id":"` + rootID + `","thread_id":"` + rootID + `"}`
	resolve := func(metadata []byte) (string, string) {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{ClientMetadata: metadata}}
		return resolveNewAPIPolicyRootSessionID(context, info, "")
	}

	depthThree := []byte(`{"client_metadata":{"client_metadata":{"client_metadata":` + leaf + `}}}`)
	if root, state := resolve(depthThree); state != newAPIPolicyRootSessionResolved || root != rootID {
		t.Fatalf("depth three root=%q state=%q", root, state)
	}
	depthFour := []byte(`{"client_metadata":{"client_metadata":{"client_metadata":{"client_metadata":` + leaf + `}}}}`)
	if root, state := resolve(depthFour); root != "" || state != newAPIPolicyRootSessionConflict {
		t.Fatalf("over-depth root=%q state=%q", root, state)
	}

	encoded := []byte(leaf)
	for range 4 {
		var err error
		encoded, err = common2.Marshal(string(encoded))
		require.NoError(t, err)
	}
	if root, state := resolve(encoded); state != newAPIPolicyRootSessionResolved || root != rootID {
		t.Fatalf("four encoded layers root=%q state=%q", root, state)
	}
	encoded, err := common2.Marshal(string(encoded))
	require.NoError(t, err)
	if root, state := resolve(encoded); root != "" || state != newAPIPolicyRootSessionConflict {
		t.Fatalf("over-encoded root=%q state=%q", root, state)
	}
}

func TestNewAPIPolicyRootSessionIDRejectsAliasAndDuplicateHeaderConflicts(t *testing.T) {
	const (
		rootID  = "019c8f60-b9e1-78c2-8fe2-91661a4f649c"
		childID = "019c8f61-071a-7932-934c-2baeaec1126c"
		otherID = "019c8f63-8612-7dd3-bb22-f2dd86e98f15"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Add("Session-Id", rootID)
	context.Request.Header.Add("Session-Id", otherID)
	if root, state := resolveNewAPIPolicyRootSessionID(context, nil, ""); root != "" || state != newAPIPolicyRootSessionConflict {
		t.Fatalf("duplicate header root=%q state=%q", root, state)
	}

	aliasConflict := `{"session_id":"` + rootID + `","thread_id":"` + childID + `","client_request_id":"` + childID + `","x_client_request_id":"` + otherID + `","parent_thread_id":"` + rootID + `"}`
	clean, _ := gin.CreateTestContext(httptest.NewRecorder())
	clean.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{ClientMetadata: []byte(aliasConflict)}}
	if root, state := resolveNewAPIPolicyRootSessionID(clean, info, ""); root != "" || state != newAPIPolicyRootSessionConflict {
		t.Fatalf("alias conflict root=%q state=%q", root, state)
	}
}

func TestNewAPIPolicyRootSessionIDRejectsInvalidStructuredEvidence(t *testing.T) {
	const (
		rootID  = "019c8f60-b9e1-78c2-8fe2-91661a4f649c"
		childID = "019c8f61-071a-7932-934c-2baeaec1126c"
	)
	for _, test := range []struct {
		name     string
		metadata string
	}{
		{
			name:     "oversized session id",
			metadata: `{"session_id":"` + strings.Repeat("x", 1025) + `","thread_id":"` + childID + `","parent_thread_id":"` + rootID + `"}`,
		},
		{
			name:     "oversized subagent marker",
			metadata: `{"session_id":"` + rootID + `","thread_id":"` + childID + `","parent_thread_id":"` + rootID + `","subagent_kind":"` + strings.Repeat("x", 65) + `"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{ClientMetadata: []byte(test.metadata)}}
			root, state := resolveNewAPIPolicyRootSessionID(c, info, "")
			assert.Empty(t, root)
			assert.Equal(t, newAPIPolicyRootSessionConflict, state)
		})
	}
}

func TestNewAPIPolicyRootSessionIDRejectsMetadataRootWithoutLeafCorroboration(t *testing.T) {
	const (
		rootID  = "019c8f60-b9e1-78c2-8fe2-91661a4f649c"
		childID = "019c8f61-071a-7932-934c-2baeaec1126c"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", childID)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"x-openai-subagent":"review","x-codex-turn-metadata":{"session_id":"` + rootID + `","parent_thread_id":"` + rootID + `","subagent_kind":"review"}}`),
	}}

	root, ok := newAPIPolicyRootSessionID(context, info, childID)
	assert.False(t, ok)
	assert.Empty(t, root)
}

func TestNewAPIPolicyRootSessionIDRejectsConflictingLineage(t *testing.T) {
	const childID = "019c8f63-8612-7dd3-bb22-f2dd86e98f15"
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", childID)
	context.Request.Header.Set("X-OpenAI-Subagent", "guardian")
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"parent_thread_id":"root-a","subagent_kind":"guardian"}`)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"x-codex-turn-metadata":{"parent_thread_id":"root-b","subagent_kind":"guardian"}}`),
	}}

	root, ok := newAPIPolicyRootSessionID(context, info, childID)
	assert.False(t, ok)
	assert.Empty(t, root)
}

func TestNewAPIPolicyRootSessionIDRejectsConflictingLeafGraph(t *testing.T) {
	const (
		rootID  = "019c8f5d-5729-7ec2-879d-da6c4f7b2301"
		childID = "019c8f5e-6b19-7bc3-bfd1-a11616b4d514"
		otherID = "019c8f5f-8ef4-7d8b-a0e2-c41f93c3b6a2"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("Thread-Id", childID)
	context.Request.Header.Set("X-Client-Request-Id", otherID)
	context.Request.Header.Set("X-Codex-Window-Id", childID+":4")
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", rootID)
	context.Request.Header.Set("X-OpenAI-Subagent", "guardian")

	root, ok := newAPIPolicyRootSessionID(context, nil, rootID)
	assert.False(t, ok)
	assert.Empty(t, root)
}

func TestNewAPIPolicyRootSessionIDRejectsMalformedWindow(t *testing.T) {
	const rootID = "019c8f5d-5729-7ec2-879d-da6c4f7b2301"
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", rootID)
	context.Request.Header.Set("X-Codex-Window-Id", rootID+":not-a-sequence")

	root, ok := newAPIPolicyRootSessionID(context, nil, rootID)
	assert.False(t, ok)
	assert.Empty(t, root)
}

func TestNewAPIPolicyRootSessionIDDoesNotTrustUncorroboratedParent(t *testing.T) {
	const childID = "019c8f64-2e4a-7ee0-a1a1-50fde3971309"
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("Session-Id", childID)
	context.Request.Header.Set("X-Codex-Parent-Thread-Id", "unconfirmed-parent")

	root, state := resolveNewAPIPolicyRootSessionID(context, nil, childID)
	assert.Empty(t, root)
	assert.Equal(t, newAPIPolicyRootSessionUnavailable, state)
}

func TestNewAPIPolicyRootSessionIDReadsMainSessionFromHeaderMetadata(t *testing.T) {
	const (
		upperRootID = "019C8F64-77D3-7A18-95B1-6711D4173F52"
		rootID      = "019c8f64-77d3-7a18-95b1-6711d4173f52"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+upperRootID+`","thread_id":"`+upperRootID+`"}`)

	root, ok := newAPIPolicyRootSessionID(context, nil, "")
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDReadsMainSessionFromBodyMetadata(t *testing.T) {
	const rootID = "019c8f64-e414-7885-9e70-f0c2911f1708"
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{
		ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + rootID + `"}`),
	}}

	root, ok := newAPIPolicyRootSessionID(context, info, "")
	require.True(t, ok)
	assert.Equal(t, rootID, root)
}

func TestNewAPIPolicyRootSessionIDRejectsMismatchedMainSessionMetadata(t *testing.T) {
	const (
		rootID   = "019c8f65-04ce-791d-a102-0cf4320cc933"
		threadID = "019c8f65-0f2d-7bbb-90ce-a390a941cc1d"
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	context.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+threadID+`"}`)

	root, ok := newAPIPolicyRootSessionID(context, nil, "")
	assert.False(t, ok)
	assert.Empty(t, root)
}

func TestNewAPIPolicyRootSessionFingerprintIsBindingIndependentAndSubjectScoped(t *testing.T) {
	const rootID = "019c8f65-6fa4-7bd1-89a9-2284899360ca"
	first := newAPIPolicyRootSessionFingerprint("newapi", "42", rootID)
	repeat := newAPIPolicyRootSessionFingerprint("NEWAPI", "42", rootID)
	otherUser := newAPIPolicyRootSessionFingerprint("newapi", "43", rootID)
	otherPlatform := newAPIPolicyRootSessionFingerprint("secondary", "42", rootID)

	require.Len(t, first, 32)
	assert.Equal(t, first, repeat)
	assert.NotEqual(t, first, otherUser)
	assert.NotEqual(t, first, otherPlatform)
}

func TestNewAPIPolicyRootSessionFingerprintProtocolVector(t *testing.T) {
	const rootID = "01a031a2-043b-7f42-afa6-ce5491d9be64"
	assert.Equal(t, "a68d950522466e5efa03ef5a2e9b9314", newAPIPolicyRootSessionFingerprint("newapi", "42", rootID))
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
