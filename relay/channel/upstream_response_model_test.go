package channel

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamResponseModelHTTPTransportPreservesBytesAndResetsRetries(t *testing.T) {
	previous := common.UpstreamResponseModelLogEnabled.Load()
	t.Cleanup(func() { common.UpstreamResponseModelLogEnabled.Store(previous) })
	payload := "data: {\"type\":\"response.created\",\"response\":{\"model\":\"initial\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5.6-luna\",\"usage\":{\"input_tokens\":8,\"output_tokens\":2}}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Test-Upstream", "unchanged")
		if r.URL.Path == "/missing" {
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(server.Close)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "public-model", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"}}
	for _, tc := range []struct {
		enabled                   bool
		path, expectedBody, model string
	}{
		{false, "/responses", payload, ""},
		{true, "/responses", payload, "gpt-5.6-luna"},
		{true, "/missing", "data: {\"type\":\"response.completed\"}\n\n", ""},
	} {
		common.UpstreamResponseModelLogEnabled.Store(tc.enabled)
		c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader("{}"))
		req, err := http.NewRequest("POST", server.URL+tc.path, strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := DoRequest(c, req, info)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, tc.expectedBody, string(body))
		assert.Equal(t, "unchanged", resp.Header.Get("X-Test-Upstream"))
		name, conflict := common.GetUpstreamResponseModel(c)
		assert.Equal(t, tc.model, name)
		assert.Equal(t, tc.model != "", conflict)
		assert.Equal(t, "public-model", info.OriginModelName)
		assert.Equal(t, "mapped-model", info.UpstreamModelName)
	}
}

func TestUpstreamResponseModelFragmentedStreamsAndJSON(t *testing.T) {
	previous := common.UpstreamResponseModelLogEnabled.Load()
	t.Cleanup(func() { common.UpstreamResponseModelLogEnabled.Store(previous) })
	common.UpstreamResponseModelLogEnabled.Store(true)
	for _, tc := range []struct{ name, mime, body, want string }{
		{"JSON", "application/json", `{"model":"gpt-6-astra","usage":{"total_tokens":10}}`, "gpt-6-astra"},
		{"multiline SSE", "text/event-stream", ": keepalive\r\nevent: response.completed\r\ndata: {\r\ndata: \"response\":{\"model\":\"gpt-5.6-sol\"}}\r\n\r\ndata: [DONE]\r\n\r\n", "gpt-5.6-sol"},
		{"EOF without delimiter", "text/event-stream", "data: {\"model\":\"gpt-5.6-luna\"}", "gpt-5.6-luna"},
		{"binary", "audio/wav", `{"model":"not-json-response"}`, ""},
		{"incomplete JSON", "application/json", `{"model":"gpt-6-astra"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.mime}}, Body: io.NopCloser(iotest.OneByteReader(strings.NewReader(tc.body)))}
			observeResponseModelBody(resp, common.BeginUpstreamResponseModel(c))
			actual, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(actual))
			name, _ := common.GetUpstreamResponseModel(c)
			assert.Equal(t, tc.want, name)
		})
	}
}

func TestUpstreamResponseModelOversizedEventDoesNotBreakFollowingResponse(t *testing.T) {
	previous := common.UpstreamResponseModelLogEnabled.Load()
	t.Cleanup(func() { common.UpstreamResponseModelLogEnabled.Store(previous) })
	common.UpstreamResponseModelLogEnabled.Store(true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	payload := []byte("data: {\"delta\":\"" + strings.Repeat("x", responseModelObservationLimit) + "\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\"}}\n\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader(payload))}
	observeResponseModelBody(resp, common.BeginUpstreamResponseModel(c))
	actual, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, payload, actual)
	name, _ := common.GetUpstreamResponseModel(c)
	assert.Equal(t, "gpt-6-astra", name)
}
