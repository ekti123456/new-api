package channel

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexUpstreamErrorSenderWireFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/upstream_error_diagnostic_v1.txt")
	require.NoError(t, err)
	request := newAPIPolicyRequestContext{RequestID: "error-fixture", ChannelID: 7, UserID: 42, PlatformID: "test-platform", Secret: "integration-secret"}
	diagnostic, err := decodeCodexUpstreamError(strings.TrimSpace(string(fixture)), request, time.Unix(1788566400, 0))
	require.NoError(t, err)
	assert.Equal(t, "read tcp: connection reset by peer", diagnostic.Message)
	assert.Equal(t, "ws_read", diagnostic.Stage)
	assert.Equal(t, 101, diagnostic.HandshakeStatus)
	assert.Zero(t, diagnostic.HTTPStatus)
}

func upstreamErrorTestEnvelope(t *testing.T, r newAPIPolicyRequestContext, d common.CodexUpstreamError) string {
	t.Helper()
	payload, err := common.Marshal(d)
	require.NoError(t, err)
	plain := make([]byte, 2048)
	binary.BigEndian.PutUint16(plain, uint16(len(payload)))
	copy(plain[2:], payload)
	key := hmac.New(sha256.New, []byte(r.Secret))
	key.Write([]byte("codex2api:error-diagnostic:v1"))
	block, err := aes.NewCipher(key.Sum(nil))
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, aead.NonceSize())
	aad := strings.Join([]string{"codex2api:error-diagnostic:v1", r.RequestID, strconv.Itoa(r.UserID), r.PlatformID}, "\n")
	return "v1." + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte(aad)))
}

func TestCodexUpstreamErrorTransports(t *testing.T) {
	for _, protocol := range []string{"http", "sse", "sse_large", "ws", "ws_failed", "ws_flat"} {
		for _, trusted := range []bool{false, true} {
			t.Run(protocol+strconv.FormatBool(trusted), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set(common.RequestIdKey, "cause-test")
				r := newAPIPolicyRequestContext{RequestID: "cause-test", ChannelID: 7, UserID: 42, PlatformID: "test", Secret: "integration-secret", ErrorAttempt: common.CodexUpstreamErrorAttemptForContext(c)}
				d := common.CodexUpstreamError{RequestID: r.RequestID, ChannelID: 7, IssuedAt: time.Now().Unix(), Message: "connection reset by peer", Source: "transport", Stage: "ws_read", Transport: "websocket"}
				encoded := upstreamErrorTestEnvelope(t, r, d)
				upstream := httptest.NewRequest("POST", "/v1/responses", nil)
				if trusted {
					upstream = upstream.WithContext(context.WithValue(upstream.Context(), newAPIPolicyRequestContextKey{}, r))
					c.Set(newAPIPolicyRequestContextGinKey, r)
				}
				public := `{"error":{"code":"internal_error","message":"Upstream request failed"}}`
				var output []byte
				if strings.HasPrefix(protocol, "ws") {
					message := `{"type":"error","error":{"code":"internal_error","message":"Upstream request failed","details":{"codex2api_error":"` + encoded + `"}}}`
					if protocol == "ws_failed" {
						message = `{"type":"response.failed","response":{"error":{"code":"internal_error","details":{"codex2api_error":"` + encoded + `"}}}}`
					}
					if protocol == "ws_flat" {
						message = `{"type":"error","code":"internal_error","message":"Upstream request failed","details":{"codex2api_error":"` + encoded + `"}}`
					}
					output = SanitizeCodexDispatchWebSocketMessage(c, []byte(message))
				} else {
					response := &http.Response{StatusCode: 500, Header: make(http.Header), Request: upstream, Body: io.NopCloser(strings.NewReader(public))}
					response.Header.Set(codexUpstreamErrorHeader, encoded)
					if strings.HasPrefix(protocol, "sse") {
						response.StatusCode = 200
						response.Header.Set("Content-Type", "text/event-stream")
						if protocol == "sse_large" {
							public = `{"response":{"error":{"code":"internal_error"},"output":"` + strings.Repeat("x", 20000) + `"},"type":"response.failed"}`
						}
						response.Body = io.NopCloser(iotest.OneByteReader(strings.NewReader(": codex2api_error " + encoded + "\n\ndata: " + public + "\n\n")))
					}
					processNewAPIPolicyResponse(c, response)
					assert.Empty(t, response.Header.Get(codexUpstreamErrorHeader))
					var err error
					output, err = io.ReadAll(response.Body)
					require.NoError(t, err)
				}
				assert.NotContains(t, string(output), encoded)
				assert.NotContains(t, string(output), "connection reset")
				assert.Contains(t, string(output), "internal_error")
				got, ok := common.GetCodexUpstreamError(c, 7)
				assert.Equal(t, trusted, ok)
				if trusted {
					assert.Equal(t, d, got)
					assert.Zero(t, got.HTTPStatus, "gateway 500 must not become an observed upstream HTTP status")
				}
			})
		}
	}
}

func TestCodexUpstreamErrorRejectsWrongScopeAndSuccess(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, "request")
	r := newAPIPolicyRequestContext{RequestID: "request", ChannelID: 7, UserID: 42, PlatformID: "test", Secret: "secret", ErrorAttempt: common.CodexUpstreamErrorAttemptForContext(c)}
	now := time.Now()
	d := common.CodexUpstreamError{RequestID: r.RequestID, ChannelID: 7, IssuedAt: now.Unix(), Message: "upstream busy", Source: "upstream_http", Stage: "http_response", HTTPStatus: 500}
	encoded := upstreamErrorTestEnvelope(t, r, d)
	for _, field := range []string{"secret", "request", "user", "platform", "channel", "expired", "future", "tampered"} {
		t.Run(field, func(t *testing.T) {
			wrong, clock, carrier := r, now, encoded
			switch field {
			case "secret":
				wrong.Secret = "another"
			case "request":
				wrong.RequestID = "another"
			case "user":
				wrong.UserID++
			case "platform":
				wrong.PlatformID = "another"
			case "channel":
				wrong.ChannelID++
			case "expired":
				clock = now.Add(61 * time.Second)
			case "future":
				clock = now.Add(-11 * time.Second)
			case "tampered":
				carrier = encoded[:30] + "!" + encoded[31:]
			}
			_, err := decodeCodexUpstreamError(carrier, wrong, clock)
			require.Error(t, err)
		})
	}
	response := &http.Response{StatusCode: 200, Header: http.Header{codexUpstreamErrorHeader: []string{encoded}}}
	processCodexUpstreamErrorHeader(response, r)
	_, ok := common.GetCodexUpstreamError(c, 7)
	assert.False(t, ok)
	longLine := "data: " + strings.Repeat("x", 20000) + "\n\n"
	stream := ": codex2api_error " + encoded + "\n\ndata: {\"type\":\"response.completed\"}\n\n" + longLine
	upstream := httptest.NewRequest("POST", "/v1/responses", nil).WithContext(context.WithValue(context.Background(), newAPIPolicyRequestContextKey{}, r))
	response = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Request: upstream, Body: io.NopCloser(strings.NewReader(stream))}
	processNewAPIPolicyResponse(c, response)
	output, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(output), "codex2api_error")
	assert.Contains(t, string(output), longLine)
	_, ok = common.GetCodexUpstreamError(c, 7)
	assert.False(t, ok)
}
