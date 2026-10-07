package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesWSRoundTripFieldFidelity(t *testing.T) {
	old := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = old })
	for _, passthrough := range []bool{false, true} {
		body := []byte(`{"type":"response.create","stream_id":"lane-1","generate":false,"model":"gpt-6.1-sol","store":false,"parallel_tool_calls":false,"max_tool_calls":0,"temperature":0,"top_p":0,"reasoning":{"effort":"low","summary":"auto","mode":"pro","context":[]},"client_metadata":{"x-codex-turn-metadata":"{\"turn_id\":\"turn-2\"}"},"access_programs":[],"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,IMAGE","detail":"original"},{"type":"input_file","filename":"原件.pdf","file_data":"data:application/pdf;base64,PDF"}],"fixture":{"id":9007199254740993,"empty":null}},{"type":"function_call","name":"js","namespace":"functions","call_id":"call-1","arguments":"{\"id\":9007199254740993}"}],"tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"js","strict":false,"parameters":{"type":"object","properties":{"id":{"const":9007199254740993}}}}]}]}`)
		original := append([]byte(nil), body...)
		for _, wrapped := range []bool{false, true, false} {
			if wrapped {
				body = []byte(`{"type":"response.create","response":` + string(body) + `}`)
			}
			event, stream, err := parseResponsesWSEnvelope(body)
			require.NoError(t, err)
			create, err := normalizeResponsesWSCreateEvent(body, event, stream)
			require.NoError(t, err)
			c, info := responsesFidelityContext(t, create.Body, "http://fixture.invalid", passthrough)
			var apiErr *types.NewAPIError
			body, apiErr = buildResponsesWSCreatePayload(c, info, create.Request, create.Generate, create.StreamID)
			require.Nil(t, apiErr)
			for _, path := range []string{"input", "tools", "store", "parallel_tool_calls", "max_tool_calls", "temperature", "top_p", "reasoning", "client_metadata", "access_programs", "generate", "stream_id"} {
				require.JSONEq(t, gjson.GetBytes(original, path).Raw, gjson.GetBytes(body, path).Raw, path)
			}
			require.Equal(t, "9007199254740993", gjson.GetBytes(body, "input.0.fixture.id").Raw)
			require.Equal(t, "9007199254740993", gjson.GetBytes(body, "tools.0.tools.0.parameters.properties.id.const").Raw)
			require.False(t, gjson.GetBytes(body, "stream").Exists())
		}
	}
}

func responsesFidelityContext(t *testing.T, body []byte, baseURL string, passthrough bool) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("responses_websocket", true)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "selected-channel-key")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-6.1-sol")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: true})
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeResponses, RelayFormat: types.RelayFormatOpenAIResponses, OriginModelName: "gpt-6.1-sol", RequestURLPath: "/v1/responses"}
}

func TestResponsesWSHandshakePreservesBetaAndChannelCredential(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		passthrough bool
		want        string
	}{
		{"adapted", false, "responses_websockets=2026-02-06"},
		{"passthrough", true, "responses_websockets=2026-02-06"},
		{"override", false, "channel-beta"},
		{"delete", false, ""},
	} {
		received := make(chan http.Header, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			received <- r.Header.Clone()
		}))
		t.Cleanup(server.Close)
		body := []byte(`{"model":"gpt-6.1-sol","input":[]}`)
		c, info := responsesFidelityContext(t, body, server.URL, scenario.passthrough)
		if scenario.name == "override" {
			common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{"openai-beta": "channel-beta"})
		}
		if scenario.name == "delete" {
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{map[string]any{"mode": "delete_header", "path": "OpenAI-Beta"}}})
		}
		c.Request.Header.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
		c.Request.Header.Set("Authorization", "Bearer client-key-must-not-leak")
		c.Request.Header.Set("Thread-Id", "original-thread")
		var req dto.OpenAIResponsesRequest
		require.NoError(t, common.Unmarshal(body, &req))
		adaptor, reader, closer, apiErr := PrepareResponsesRequest(c, info, &req)
		require.Nil(t, apiErr)
		_, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, closer.Close())
		conn, err := relaychannel.DoWssRequest(adaptor, c, info, nil)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		headers := <-received
		require.Equal(t, scenario.want, headers.Get("OpenAI-Beta"), scenario.name)
		require.Equal(t, "Bearer selected-channel-key", headers.Get("Authorization"))
		require.Equal(t, "original-thread", headers.Get("Thread-Id"))
	}
}
