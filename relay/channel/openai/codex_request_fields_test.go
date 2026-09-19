package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesConversionPreservesCodexRequestControls(t *testing.T) {
	for _, tc := range []struct{ name, generate string }{
		{"prewarm", `,"generate":false`},
		{"generate", `,"generate":true`},
		{"omitted", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"gpt-5.6-sol","input":[],"access_programs":{"cyber":"standard"},"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"prewarm\"}"}` + tc.generate + `}`
			var request dto.OpenAIResponsesRequest
			require.NoError(t, common.Unmarshal([]byte(body), &request))
			copied, err := common.DeepCopy(&request)
			require.NoError(t, err)
			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, &relaycommon.RelayInfo{}, *copied)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.Equal(t, gjson.Get(body, "generate").Exists(), gjson.GetBytes(wire, "generate").Exists())
			assert.Equal(t, gjson.Get(body, "generate").Bool(), gjson.GetBytes(wire, "generate").Bool())
			assert.JSONEq(t, `{"cyber":"standard"}`, gjson.GetBytes(wire, "access_programs").Raw)
			assert.Equal(t, `{"request_kind":"prewarm"}`, gjson.GetBytes(wire, "client_metadata.x-codex-turn-metadata").String())
		})
	}
}
