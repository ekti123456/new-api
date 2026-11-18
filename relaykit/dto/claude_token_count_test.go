package dto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeToolResultMediaTokenMetadata(t *testing.T) {
	payload := strings.Repeat("a0Z9+/b1", 250000)
	for _, tc := range []struct {
		name, kind, sourceType, data, url, mime string
		fileType                                types.FileType
	}{
		{"inline image", "image", "base64", payload, "", "image/jpeg", types.FileTypeImage},
		{"remote image", "image", "url", "", "https://example.invalid/image.png", "image/png", types.FileTypeImage},
		{"inline document", "document", "base64", "JVBERi0xLjQ=", "", "application/pdf", types.FileTypeFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := map[string]any{"type": tc.kind, "source": map[string]any{"type": tc.sourceType, "data": tc.data, "url": tc.url, "media_type": tc.mime}}
			var reference *types.TokenCountMeta
			for _, nested := range []bool{false, true} {
				var block any = image
				if nested {
					block = map[string]any{"type": "tool_result", "tool_use_id": "tool_1", "content": []any{image}}
				}
				request := &ClaudeRequest{Model: "claude-opus-5-5", Messages: []ClaudeMessage{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "Read this"}, block}}}}
				before, err := kitutil.Marshal(request)
				require.NoError(t, err)
				meta := request.GetTokenCountMeta()
				require.Len(t, meta.Files, 1)
				assert.Equal(t, tc.fileType, meta.Files[0].FileType)
				assert.Equal(t, tc.url != "", meta.Files[0].Source.IsURL())
				if tc.url == "" {
					assert.Equal(t, tc.data, meta.Files[0].Source.GetRawData())
				} else {
					assert.Equal(t, tc.url, meta.Files[0].Source.GetRawData())
				}
				assert.Equal(t, "user\nRead this", meta.CombineText, "encoded media and protocol fields are not model text")
				after, err := kitutil.Marshal(request)
				require.NoError(t, err)
				assert.Equal(t, string(before), string(after), "estimation must not change the forwarded request")
				if reference == nil {
					reference = meta
				} else {
					assert.Equal(t, reference.CombineText, meta.CombineText)
					assert.Equal(t, reference.MessagesCount, meta.MessagesCount)
				}
			}
		})
	}
}

func TestClaudeToolResultTextAndTypedContent(t *testing.T) {
	text := "line one\nline two: {\"data\":\"a0Z9+/b1\"}"
	for _, tc := range []struct {
		name    string
		content any
	}{
		{"plain tool result", text},
		{"decoded content blocks", []any{map[string]any{"type": "text", "text": text}}},
		{"typed content blocks", []ClaudeMediaMessage{{Type: "text", Text: &text}}},
		{"raw content blocks", json.RawMessage(`[{"type":"text","text":"line one\nline two: {\"data\":\"a0Z9+/b1\"}"}]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: []ClaudeMediaMessage{{Type: "tool_result", Content: tc.content}}}}}
			meta := request.GetTokenCountMeta()
			assert.Equal(t, "user\n"+text, meta.CombineText, "real text must not be JSON-escaped or dropped because it resembles encoded data")
			assert.Empty(t, meta.Files)
		})
	}
}

func TestClaudeTokenMetadataCountsNestedTextOnceAndSkipsOpaqueHistory(t *testing.T) {
	text := "nested output"
	request := &ClaudeRequest{
		System: []any{map[string]any{"type": "text", "text": "system instructions"}},
		Messages: []ClaudeMessage{{Role: "user", Content: []any{
			map[string]any{"type": "tool_result", "content": []any{
				map[string]any{"type": "tool_result", "content": []any{map[string]any{"type": "text", "text": text}}},
				map[string]any{"type": "thinking", "thinking": "opaque reasoning", "signature": "secret signature"},
				map[string]any{"type": "redacted_thinking", "data": "opaque encrypted history"},
				map[string]any{"type": "image", "source": nil},
				nil,
			}},
		}}},
	}
	meta := request.GetTokenCountMeta()
	assert.Equal(t, "system instructions\nuser\n"+text, meta.CombineText)
	assert.Empty(t, meta.Files)
}

var claudeTokenMetadataBenchmark *types.TokenCountMeta

func BenchmarkClaudeToolResultImageMetadata(b *testing.B) {
	payload := strings.Repeat("a0Z9+/b1", 250000)
	request := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: []any{
		map[string]any{"type": "tool_result", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": payload}},
		}},
	}}}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claudeTokenMetadataBenchmark = request.GetTokenCountMeta()
	}
}
