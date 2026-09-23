package channel

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestTypePreservesIngressAfterParentAssociation(t *testing.T) {
	const root = "01a06000-0000-7000-8000-000000000001"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":"normal input"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Session-Id", root)
	c.Request.Header.Set("Thread-Id", root)
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+root+`","thread_source":"thread_title","request_kind":"turn"}`)
	ResolveCodexRootSessionForDistribution(c)
	before, ok := common.GetCodexRequestClassification(c)
	require.True(t, ok)
	require.Equal(t, "independent_internal", before.Type)
	require.True(t, SetCodexPassiveRootSessionOverride(c, root, "related_internal"))
	// This must already be recorded when an error interrupts before dispatch.
	after, ok := common.GetCodexRequestClassification(c)
	require.True(t, ok)
	require.Equal(t, "related_internal", after.Type)
	require.Equal(t, "independent_internal", after.IngressType)
	ResolveCodexRootSessionForDistribution(c)
	after, _ = common.GetCodexRequestClassification(c)
	require.Equal(t, "related_internal", after.Type)
	require.Equal(t, "independent_internal", after.IngressType)
	require.Equal(t, "thread_title", after.ThreadSource)
}

func TestRequestTypeCompactionIsProtocolBased(t *testing.T) {
	for _, tc := range []struct{ name, path, body, want string }{
		{"compact endpoint", "/v1/responses/compact", `{"input":"summary"}`, "compaction"},
		{"native trigger", "/v1/responses", `{"compaction_trigger":{},"input":"summary"}`, "compaction"},
		{"quoted trigger is user text", "/v1/responses", `{"input":"compaction_trigger guardian_review"}`, "unknown"},
		{"null trigger", "/v1/responses", `{"compaction_trigger":null}`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			ResolveCodexRootSessionForDistribution(c)
			got, ok := common.GetCodexRequestClassification(c)
			require.True(t, ok)
			require.Equal(t, tc.want, got.Type)
		})
	}
}
