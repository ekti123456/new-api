package helper

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamScannerPreservesCodexFunctionalHeaders(t *testing.T) {
	for _, reasoning := range []string{"true", ""} {
		t.Run("reasoning="+reasoning, func(t *testing.T) {
			c, resp, info := setupStreamTest(t, strings.NewReader("data: [DONE]\n\n"))
			resp.Header = map[string][]string{
				"Openai-Model":         {"gpt-5.6-sol"},
				"X-Reasoning-Included": {reasoning},
				"X-Models-Etag":        {"scoped-model-version"},
				"X-Codex-Turn-State":   {"client-turn-alias"},
				"X-Request-Id":         {"private-upstream-request"},
				"Chatgpt-Account-Id":   {"private-upstream-account"},
				"Set-Cookie":           {"private-upstream-cookie"},
			}
			StreamScannerHandler(c, resp, info, func(string, *StreamResult) {})
			assert.Equal(t, "gpt-5.6-sol", c.Writer.Header().Get("OpenAI-Model"))
			assert.Equal(t, []string{reasoning}, c.Writer.Header().Values("X-Reasoning-Included"))
			assert.Equal(t, "scoped-model-version", c.Writer.Header().Get("X-Models-Etag"))
			assert.Equal(t, "client-turn-alias", c.Writer.Header().Get("X-Codex-Turn-State"))
			for _, private := range []string{"X-Request-Id", "Chatgpt-Account-Id", "Set-Cookie"} {
				assert.Empty(t, c.Writer.Header().Values(private))
			}
		})
	}
}
