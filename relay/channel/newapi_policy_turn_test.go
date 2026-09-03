package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveCodexRootSessionCollectsTurnLineageAndCanonicalFork(t *testing.T) {
	const (
		temporaryRoot = "01a06000-0000-7000-8000-000000000001"
		sourceRoot    = "01a06000-0000-7000-8000-000000000002"
		turnID        = "01a06000-0000-7000-8000-000000000003"
		parentTurnID  = "01a06000-0000-7000-8000-000000000004"
		rootTurnID    = "01a06000-0000-7000-8000-000000000005"
	)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-luna","input":"title"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Session-Id", temporaryRoot)
	c.Request.Header.Set("Thread-Id", temporaryRoot)
	c.Request.Header.Set("X-Client-Request-Id", temporaryRoot)
	c.Request.Header.Set("X-Codex-Window-Id", temporaryRoot+":0")
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+temporaryRoot+`","thread_id":"`+temporaryRoot+`","window_id":"`+temporaryRoot+`:0","forked_from_thread_id":"`+sourceRoot+`","turn_id":"`+turnID+`","parent_turn_id":"`+parentTurnID+`","root_turn_id":"`+rootTurnID+`","turn_trigger":"thread_title","thread_source":"thread_title","request_kind":"turn"}`)

	resolution := ResolveCodexRootSessionForDistribution(c)
	require.True(t, resolution.Resolved)
	require.True(t, resolution.Related)
	require.False(t, resolution.TurnLineageConflict)
	require.Equal(t, temporaryRoot, resolution.RootID)
	require.Equal(t, temporaryRoot, resolution.ThreadID)
	require.Equal(t, sourceRoot, resolution.ForkedFromID)
	require.Equal(t, turnID, resolution.TurnID)
	require.Equal(t, parentTurnID, resolution.ParentTurnID)
	require.Equal(t, rootTurnID, resolution.RootTurnID)
	require.Equal(t, "thread_title", resolution.TurnTrigger)
	feature, classified := ClassifyForkedCodexNamingRequest(resolution)
	require.True(t, classified)
	require.Equal(t, "related_internal", feature)
}

func TestForkedCodexNamingRequiresCanonicalMetadataField(t *testing.T) {
	const (
		temporaryRoot = "01a06000-0000-7000-8000-000000000011"
		sourceRoot    = "01a06000-0000-7000-8000-000000000012"
	)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-luna","input":"title"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Session-Id", temporaryRoot)
	c.Request.Header.Set("Thread-Id", temporaryRoot)
	c.Request.Header.Set("X-Client-Request-Id", temporaryRoot)
	c.Request.Header.Set("X-Codex-Window-Id", temporaryRoot+":0")
	c.Request.Header.Set("X-Codex-Forked-From-Thread-Id", sourceRoot)
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+temporaryRoot+`","thread_id":"`+temporaryRoot+`","window_id":"`+temporaryRoot+`:0","thread_source":"thread_title","request_kind":"turn"}`)

	resolution := ResolveCodexRootSessionForDistribution(c)
	require.True(t, resolution.Resolved)
	require.False(t, resolution.Related)
	require.Empty(t, resolution.ForkedFromID)
	_, classified := ClassifyForkedCodexNamingRequest(resolution)
	require.False(t, classified)
}

func TestResolveCodexRootSessionCollectsEmbeddedClientTurnMetadata(t *testing.T) {
	const (
		rootID       = "01a06000-0000-7000-8000-000000000021"
		turnID       = "01a06000-0000-7000-8000-000000000022"
		parentTurnID = "01a06000-0000-7000-8000-000000000023"
		rootTurnID   = "01a06000-0000-7000-8000-000000000024"
	)
	body := `{"model":"gpt-5.6-sol","input":"child","client_metadata":{"session_id":"` + rootID + `","thread_id":"` + rootID + `","x-codex-turn-metadata":"{\"session_id\":\"` + rootID + `\",\"thread_id\":\"` + rootID + `\",\"turn_id\":\"` + turnID + `\",\"parent_turn_id\":\"` + parentTurnID + `\",\"root_turn_id\":\"` + rootTurnID + `\",\"turn_trigger\":\"composer\"}"}}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	resolution := ResolveCodexRootSessionForDistribution(c)
	require.True(t, resolution.Resolved)
	require.False(t, resolution.TurnLineageConflict)
	require.Equal(t, turnID, resolution.TurnID)
	require.Equal(t, parentTurnID, resolution.ParentTurnID)
	require.Equal(t, rootTurnID, resolution.RootTurnID)
	require.Equal(t, "composer", resolution.TurnTrigger)
	require.Equal(t, rootID, resolution.ThreadID)
}

func TestResolveCodexRootSessionReadsCompactionClientMetadata(t *testing.T) {
	const (
		rootID = "01a06000-0000-7000-8000-000000000025"
		turnID = "01a06000-0000-7000-8000-000000000026"
	)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesCompactionRequest{
		ClientMetadata: []byte(`{"session_id":"` + rootID + `","thread_id":"` + rootID + `","turn_id":"` + turnID + `","thread_source":"user","request_kind":"compact"}`),
	}}

	resolution := analyzeNewAPIPolicyRootSession(c, info, newAPIPolicyStableSessionID(c, info))
	require.Equal(t, newAPIPolicyRootSessionResolved, resolution.state)
	require.Equal(t, rootID, resolution.rootID)
	require.Equal(t, rootID, resolution.threadID)
	require.Equal(t, turnID, resolution.turnID)
	require.Equal(t, "user", resolution.threadSource)
	require.Equal(t, "compact", resolution.requestKind)
}

func TestResolveCodexRootSessionMarksConflictingTurnLineageWithoutBreakingExplicitGraph(t *testing.T) {
	const (
		rootID = "01a06000-0000-7000-8000-000000000031"
		turnA  = "01a06000-0000-7000-8000-000000000032"
		turnB  = "01a06000-0000-7000-8000-000000000033"
	)
	body := `{"model":"gpt-5.6-sol","input":"root","client_metadata":{"session_id":"` + rootID + `","thread_id":"` + rootID + `","turn_id":"` + turnB + `"}}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Session-Id", rootID)
	c.Request.Header.Set("Thread-Id", rootID)
	c.Request.Header.Set("X-Client-Request-Id", rootID)
	c.Request.Header.Set("X-Codex-Window-Id", rootID+":0")
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+rootID+`","thread_id":"`+rootID+`","turn_id":"`+turnA+`"}`)

	resolution := ResolveCodexRootSessionForDistribution(c)
	require.True(t, resolution.Resolved)
	require.Equal(t, rootID, resolution.RootID)
	require.True(t, resolution.TurnLineageConflict)
	require.Empty(t, resolution.TurnID)
}

func TestUnlinkedThreadSummaryClassificationRemainsTemporalOnly(t *testing.T) {
	resolution := CodexRootSessionResolution{
		RootID: "01a06000-0000-7000-8000-000000000041", Resolved: true, ThreadSource: "thread_summary",
	}
	feature, classified := ClassifyUnlinkedCodexThreadSummaryRequest(resolution)
	require.True(t, classified)
	require.Equal(t, "related_internal", feature)

	resolution.Related = true
	_, classified = ClassifyUnlinkedCodexThreadSummaryRequest(resolution)
	require.False(t, classified)
}

func TestTurnRootOverrideSignsRestoredRetryRole(t *testing.T) {
	const (
		secret = "0123456789abcdef0123456789abcdef"
		rootID = "01a06000-0000-7000-8000-000000000051"
	)
	apiKey := "sk-turn-role"
	keyDigest := sha256.Sum256([]byte(apiKey))
	binding := newAPIPolicyBinding{
		PlatformID: "primary-newapi", Target: "http://127.0.0.1:18095",
		CodexKeyFingerprint: hex.EncodeToString(keyDigest[:]), Secret: secret, Enabled: true,
	}
	configurePolicyTest(t, []newAPIPolicyBinding{binding})

	tests := []struct {
		name           string
		related        bool
		feature        string
		threadSource   string
		requestKind    string
		subagentKind   string
	}{
		{name: "system passive", related: true, feature: "system_passive", threadSource: "system", requestKind: "turn"},
		{name: "user compaction owner", related: true, threadSource: "user", requestKind: "compaction"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/responses", nil)
			c.Request.RemoteAddr = "203.0.113.9:4567"
			require.True(t, SetCodexTurnRootSessionOverride(
				c, rootID, test.related, test.feature, test.threadSource, test.requestKind, test.subagentKind,
			))

			body := []byte(`{"model":"gpt-5.6-sol","input":"retry","client_metadata":{"turn_id":"01a06000-0000-7000-8000-000000000052"}}`)
			req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18095/v1/responses", bytes.NewReader(body))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{
				UserId: 42, UserGroup: "pro", RequestId: "req-turn-role-" + test.name,
				OriginModelName: "gpt-5.6-sol", RelayFormat: types.RelayFormatOpenAIResponses,
				FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: apiKey, UpstreamModelName: "gpt-5.6-sol"},
				Request:     &dto.OpenAIResponsesRequest{ClientMetadata: []byte(`{"turn_id":"01a06000-0000-7000-8000-000000000052"}`)},
			}
			require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
			payload, err := base64.RawURLEncoding.DecodeString(req.Header.Get("X-NewAPI-Policy-Meta"))
			require.NoError(t, err)
			var meta newAPIPolicyMeta
			require.NoError(t, common2.Unmarshal(payload, &meta))
			require.Equal(t, newAPIPolicyRootSessionResolved, meta.RootSessionState)
			require.Equal(t, newAPIPolicyRootSessionRelationRelated, meta.RootSessionRelation)
			require.Equal(t, test.threadSource, meta.ThreadSource)
			require.Equal(t, test.requestKind, meta.RequestKind)
			require.Equal(t, test.subagentKind, meta.SubagentKind)
			require.Equal(t, test.feature, meta.PassiveFeature)
			require.Equal(t, newAPIPolicyRootSessionFingerprint(binding.PlatformID, "42", rootID), meta.RootSessionFingerprint)
		})
	}
}
