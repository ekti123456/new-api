package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveCodexRootSessionPreservesCoherentWindowID(test *testing.T) {
	const rootID = "01a08952-0000-7000-8000-000000000731"
	for _, scenario := range []struct {
		name         string
		headerWindow string
		bodyWindow   string
		embedded     bool
		expected     string
	}{
		{name: "body only zero", bodyWindow: rootID + ":0", expected: rootID + ":0"},
		{name: "header only zero", headerWindow: rootID + ":0", expected: rootID + ":0"},
		{name: "embedded metadata", bodyWindow: rootID + ":71", embedded: true, expected: rootID + ":71"},
		{name: "equivalent numeric suffix", headerWindow: rootID + ":00", bodyWindow: rootID + ":0", expected: rootID + ":0"},
		{name: "missing is not zero"},
		{name: "conflicting suffix", headerWindow: rootID + ":0", bodyWindow: rootID + ":1"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			metadata := map[string]any{"session_id": rootID, "thread_id": rootID, "thread_source": "user", "request_kind": "turn"}
			if scenario.bodyWindow != "" {
				metadata["window_id"] = scenario.bodyWindow
			}
			clientMetadata := metadata
			if scenario.embedded {
				encoded, err := common.Marshal(metadata)
				require.NoError(test, err)
				clientMetadata = map[string]any{"x-codex-turn-metadata": string(encoded)}
			}
			body, err := common.Marshal(map[string]any{"model": "gpt-5.6-sol", "client_metadata": clientMetadata})
			require.NoError(test, err)
			requestContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			requestContext.Request.Header.Set("Content-Type", "application/json")
			if scenario.headerWindow != "" {
				requestContext.Request.Header.Set("X-Codex-Window-Id", scenario.headerWindow)
			}
			resolution := ResolveCodexRootSessionForDistribution(requestContext)
			require.True(test, resolution.Resolved)
			require.Equal(test, rootID, resolution.RootID)
			require.Equal(test, scenario.expected, resolution.WindowID)
		})
	}
}
