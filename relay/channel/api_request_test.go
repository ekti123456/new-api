package channel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCPAIdentityPreservesNonReplayableBody(t *testing.T) {
	t.Setenv("CPA_IDENTITY_INSTANCE_ID", "persistent-instance")
	for _, tc := range []struct {
		name, payload, headerDevice, turnMetadata, wantDevice string
		cached                                                bool
	}{
		{"body installation", `{"client_metadata":{"installation_id":"body-device"}}`, "", "", "body-device", true},
		{"body device", `{"metadata":{"device_id":"metadata-device"}}`, "", "", "metadata-device", true},
		{"header priority", `{"client_metadata":{"device_id":"body-device"}}`, "header-device", "", "header-device", true},
		{"turn metadata", `{}`, "", `{"installation_id":"turn-device"}`, "turn-device", false},
		{"no optional fields", `{}`, "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.payload))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("X-Device-Id", tc.headerDevice)
			c.Request.Header.Set("X-Codex-Turn-Metadata", tc.turnMetadata)
			if tc.cached {
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				defer storage.Close()
				_, err = storage.Seek(3, io.SeekStart)
				require.NoError(t, err)
			}
			// No GetBody: identity forwarding must neither reject nor drain uploads.
			req, err := http.NewRequest(http.MethodPost, "https://cpa.example/v1/responses", io.NopCloser(strings.NewReader(tc.payload)))
			require.NoError(t, err)
			require.Nil(t, req.GetBody)
			info := &relaycommon.RelayInfo{UserId: 5, TokenId: 9}
			require.NoError(t, applyCPAIdentity(c, req, info))
			first := req.Header.Get("X-CPA-Identity")
			require.NoError(t, applyCPAIdentity(c, req, info))
			assert.Equal(t, first, req.Header.Get("X-CPA-Identity"), "retry must keep identity")
			decoded, err := base64.RawURLEncoding.DecodeString(first)
			require.NoError(t, err)
			assert.Equal(t, "5", gjson.GetBytes(decoded, "user").String())
			assert.Equal(t, tc.wantDevice, gjson.GetBytes(decoded, "device").String())
			assert.Empty(t, gjson.GetBytes(decoded, "ua").String())
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			assert.Equal(t, tc.payload, string(body))
			if tc.cached {
				cached, _ := c.Get(common.KeyBodyStorage)
				offset, err := cached.(common.BodyStorage).Seek(0, io.SeekCurrent)
				require.NoError(t, err)
				assert.EqualValues(t, 3, offset, "original cached body cursor must be unchanged")
			}
			info.UserId = 0
			req.Header.Set("X-CPA-Identity-Signature", "stale")
			require.NoError(t, applyCPAIdentity(c, req, info))
			assert.Empty(t, req.Header.Get("X-CPA-Identity"), "unauthenticated state must not assert user identity")
			assert.Empty(t, req.Header.Get("X-CPA-Identity-Signature"))
		})
	}
}

func TestNewTaskAPIRequestInheritsClientCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	upstream, err := newTaskAPIRequest(c, "https://provider.example/tasks", nil)
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, upstream.Context().Err(), context.Canceled)
}

type cancellationTestAdaptor struct {
	Adaptor
	url string
}

func (a cancellationTestAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.url, nil
}
func (a cancellationTestAdaptor) SetupRequestHeader(_ *gin.Context, _ *http.Header, _ *relaycommon.RelayInfo) error {
	return nil
}

func TestRelayClientCancellationClosesUpstream(t *testing.T) {
	service.InitHttpClient()
	for _, form := range []bool{false, true} {
		for _, headers := range []bool{false, true} {
			t.Run(fmt.Sprintf("form=%t/headers=%t", form, headers), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				accepted := make(chan struct{})
				disconnected := make(chan struct{})
				forceFinish := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.ReadAll(r.Body)
					if headers {
						w.Header().Set("Content-Type", "text/event-stream")
						w.WriteHeader(200)
						_, _ = io.WriteString(w, "data: ready\n\n")
						w.(http.Flusher).Flush()
					}
					close(accepted)
					select {
					case <-r.Context().Done():
						close(disconnected)
					case <-forceFinish:
					}
				}))
				defer server.Close()
				defer close(forceFinish)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}")).WithContext(ctx)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
				finished := make(chan error, 1)
				receivedHeaders := make(chan struct{})
				go func() {
					builder := DoApiRequest
					if form {
						builder = DoFormRequest
					}
					resp, err := builder(cancellationTestAdaptor{url: server.URL}, c, info, strings.NewReader("{}"))
					if resp != nil {
						close(receivedHeaders)
						_, err = io.ReadAll(resp.Body)
						_ = resp.Body.Close()
					}
					finished <- err
				}()
				select {
				case <-accepted:
				case <-time.After(2 * time.Second):
					t.Fatal("upstream did not receive request")
				}
				if headers {
					select {
					case <-receivedHeaders:
					case <-time.After(2 * time.Second):
						t.Fatal("upstream headers were not received")
					}
				}
				cancel()
				// The test accepts cancellation either before or after the HTTP
				// headers have crossed the transport boundary.
				select {
				case <-disconnected:
				case <-time.After(2 * time.Second):
					t.Fatal("client canceled but upstream request remained running")
				}
				select {
				case err := <-finished:
					var apiErr *types.NewAPIError
					if errors.As(err, &apiErr) {
						assert.Equal(t, 499, apiErr.StatusCode)
						assert.True(t, types.IsSkipRetryError(apiErr), "canceled callers must not trigger another channel attempt")
					} else {
						require.ErrorIs(t, err, context.Canceled)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("relay did not return after cancellation")
				}
			})
		}
	}
}

type timeoutTestTransport struct{}

func (timeoutTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

func TestRelayTimeoutKeepsCauseClassificationThroughOpenAIWrapper(t *testing.T) {
	service.InitHttpClient()
	client := service.GetHttpClient()
	original := client.Transport
	client.Transport = timeoutTestTransport{}
	defer func() { client.Transport = original }()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}"))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := DoApiRequest(cancellationTestAdaptor{url: "http://127.0.0.1:1/v1/responses"}, c, info, strings.NewReader("{}"))
	require.Error(t, err)
	wrapped := types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	assert.Equal(t, http.StatusGatewayTimeout, wrapped.StatusCode)
	assert.Equal(t, types.ErrorCode("upstream_timeout"), wrapped.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(wrapped), "a live caller retains the configured timeout retry policy")
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}

func TestToWebSocketURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://api.openai.com/v1/responses":             "wss://api.openai.com/v1/responses",
		"http://127.0.0.1:3000/v1/responses":              "ws://127.0.0.1:3000/v1/responses",
		"wss://chatgpt.com/backend-api/codex/responses":   "wss://chatgpt.com/backend-api/codex/responses",
		"ws://127.0.0.1:3000/backend-api/codex/responses": "ws://127.0.0.1:3000/backend-api/codex/responses",
	} {
		assert.Equal(t, want, toWebSocketURL(input), input)
	}
}
