package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRawProtocolHeaderPassthrough(t *testing.T) {
	const header = "X-OpenAI-Internal-Codex-Responses-Lite"
	const key = "x-openai-internal-codex-responses-lite"
	pass := map[string]any{"mode": "pass_headers", "value": []any{header, "Cookie", "Authorization"}, "keep_origin": true}
	drop := map[string]any{"mode": "delete_header", "path": header}
	for _, tc := range []struct {
		name   string
		ops    []any
		static map[string]any
		want   string
		reads  int
	}{
		{"declared", []any{pass}, nil, "{client_header:" + header + "}", 0},
		{"later_deletion", []any{pass, drop}, nil, "", 0},
		{"empty_override", []any{pass}, map[string]any{header: ""}, "", 0},
		{"explicit_override", []any{pass}, map[string]any{header: "pinned"}, "pinned", 0},
		{"body_override_is_ignored", []any{map[string]any{"mode": "set", "path": "input", "value": "replacement"}, pass}, nil, "{client_header:" + header + "}", 0},
		{"conditional_match", []any{map[string]any{"mode": "pass_headers", "value": header, "conditions": []any{map[string]any{"path": "model", "mode": "full", "value": "sample"}}}}, nil, "{client_header:" + header + "}", 1},
		{"conditional_miss", []any{map[string]any{"mode": "pass_headers", "value": header, "conditions": []any{map[string]any{"path": "model", "mode": "full", "value": "other"}}}}, nil, "", 1},
		{"no_header_rule", []any{map[string]any{"mode": "set", "path": "input", "value": "different"}}, nil, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &RelayInfo{RequestHeaders: map[string]string{header: "true", "Cookie": "private", "Authorization": "private"}, ChannelMeta: &ChannelMeta{ParamOverride: map[string]any{"operations": tc.ops}, HeadersOverride: tc.static}}
			reads := 0
			err := ApplyRawProtocolHeaderPassthrough(info, []string{header}, func() ([]byte, error) { reads++; return []byte(`{"model":"sample"}`), nil })
			require.NoError(t, err)
			out := GetEffectiveHeaderOverride(info)
			if tc.want == "" {
				require.NotContains(t, out, key)
			} else {
				require.Equal(t, tc.want, out[key])
			}
			require.NotContains(t, out, "authorization")
			require.NotContains(t, out, "cookie")
			require.Equal(t, tc.reads, reads)
		})
	}
}
