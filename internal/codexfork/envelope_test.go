package codexfork

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Protocol envelope captured from the installed official desktop app. Input
// is synthetic; no login, API key, user prompt or device identifier is stored.
func TestNativeDesktopForkEnvelope(t *testing.T) {
	raw, err := os.ReadFile("testdata/desktop-forks.json")
	require.NoError(t, err)
	var captures []struct {
		Name           string            `json:"name"`
		Root           string            `json:"root"`
		Parent         string            `json:"parent"`
		Headers        map[string]string `json:"headers"`
		ClientMetadata map[string]any    `json:"client_metadata"`
		PromptCacheKey string            `json:"prompt_cache_key"`
	}
	require.NoError(t, common.Unmarshal(raw, &captures))
	for _, capture := range captures {
		t.Run(capture.Name, func(t *testing.T) {
			headers := make(http.Header)
			for key, value := range capture.Headers {
				headers.Set(key, value)
			}
			body, err := common.Marshal(map[string]any{"model": "test", "client_metadata": capture.ClientMetadata, "prompt_cache_key": capture.PromptCacheKey, "input": []any{map[string]string{"type": "compaction", "encrypted_content": "KEEP"}}})
			require.NoError(t, err)
			turn := gjson.Get(headers.Get("X-Codex-Turn-Metadata"), "turn_id").String()
			next, out, err := Project(headers, body, capture.Root, turn)
			require.NoError(t, err)
			require.Equal(t, capture.Root, next.Get("Session-Id"))
			require.NotContains(t, next.Get("X-Codex-Turn-Metadata"), "forked_from")
			require.NotContains(t, gjson.GetBytes(out, "client_metadata").Raw, capture.Parent)
			require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(out, "input").Raw)
		})
	}
	headers := http.Header{"Session-Id": {rootA}, "Conversation-Id": {turnB}}
	_, _, err = Project(headers, []byte(`{}`), rootB, turnB)
	require.Error(t, err, "a third unrelated header cannot be silently rewritten")
}

const rootB = "019a0000-0000-7000-8000-000000000002"
const rootA = "019a0000-0000-7000-8000-000000000001"
const turnB = "019a0000-0000-7000-8000-000000000003"

func TestProjectPreservesHistoryAndCreatesOneRoot(t *testing.T) {
	meta := `{"session_id":"` + rootB + `","thread_id":"` + rootB + `","forked_from_thread_id":"` + rootA + `","parent_thread_id":"` + rootA + `","forked_from_ordinal_exclusive":42,"turn_id":"` + turnB + `","root_turn_id":"` + rootA + `","parent_turn_id":"` + rootA + `","window_number":7,"window_id":"` + rootB + `:7"}`
	for _, encoded := range []bool{false, true} {
		t.Run(fmt.Sprint(encoded), func(t *testing.T) {
			nested := meta
			if encoded {
				raw, _ := common.Marshal(meta)
				nested = string(raw)
			}
			body := []byte(`{"model":"test","client_metadata":{"session_id":"` + rootB + `","thread_id":"` + rootB + `","x-codex-turn-metadata":` + nested + `},"prompt_cache_key":"` + rootA + `","input":[{"type":"compaction","encrypted_content":"KEEP_ENCRYPTED"},{"role":"user","content":"forked_from_thread_id: ` + rootA + `"},{"type":"function_call_output","call_id":"keep-call","output":{"number":9007199254740993}}],"tools":[{"type":"function","name":"forked_from_thread_id"}]}`)
			headers := http.Header{"Session-Id": {rootA}, "Thread-Id": {rootB}, "X-Codex-Turn-Metadata": {meta}}
			before := string(body)
			outHeaders, out, err := Project(headers, body, rootB, turnB)
			require.NoError(t, err)
			require.Equal(t, rootB, outHeaders.Get("Session-Id"))
			require.Equal(t, rootB, gjson.GetBytes(out, "prompt_cache_key").String())
			require.Equal(t, gjson.Get(before, "input").Raw, gjson.GetBytes(out, "input").Raw)
			require.Equal(t, gjson.Get(before, "tools").Raw, gjson.GetBytes(out, "tools").Raw)
			require.Equal(t, rootA, headers.Get("Session-Id"), "original input remains available for signature verification")
			require.Equal(t, before, string(body))
			outMeta := gjson.GetBytes(out, "client_metadata.x-codex-turn-metadata")
			if encoded {
				outMeta = gjson.Parse(outMeta.String())
			}
			require.False(t, outMeta.Get("forked_from_thread_id").Exists())
			require.False(t, outMeta.Get("parent_turn_id").Exists())
			require.Equal(t, turnB, outMeta.Get("root_turn_id").String())
			require.Equal(t, int64(7), outMeta.Get("window_number").Int())
			_, again, err := Project(outHeaders, out, rootB, turnB)
			require.NoError(t, err)
			require.Equal(t, out, again)
		})
	}
}

func TestProjectRejectsMalformedMetadata(t *testing.T) {
	for _, body := range []string{`[]`, `{}`, `{"client_metadata":{},"client_metadata":{}}`, `{"prompt_cache_key":"a","prompt_cache_key":"b"}`, `{"client_metadata":{"session_id":"a","session_id":"b"}}`, `{"client_metadata":{"x-codex-turn-metadata":"not json"}}`} {
		_, out, err := Project(nil, []byte(body), rootB, turnB)
		if body == `{}` {
			require.NoError(t, err)
			require.True(t, gjson.ValidBytes(out))
			continue
		}
		require.Error(t, err)
	}
}

func BenchmarkProjectLargeHistory(b *testing.B) {
	for _, size := range []int{8 << 10, 128 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			body := []byte(`{"client_metadata":{"session_id":"` + rootB + `","thread_id":"` + rootB + `","forked_from_thread_id":"` + rootA + `"},"prompt_cache_key":"` + rootA + `","input":[{"role":"user","content":"` + strings.Repeat("a", size) + `"}]}`)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				if _, _, err := Project(nil, body, rootB, turnB); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
