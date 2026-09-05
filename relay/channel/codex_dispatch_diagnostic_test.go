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

func dispatchDiagnosticTestEnvelope(test *testing.T, request newAPIPolicyRequestContext, diagnostic common.CodexDispatchDiagnostic) string {
	test.Helper()
	payload, err := common.Marshal(diagnostic)
	require.NoError(test, err)
	plaintext := make([]byte, 2048)
	binary.BigEndian.PutUint16(plaintext, uint16(len(payload)))
	copy(plaintext[2:], payload)
	key := hmac.New(sha256.New, []byte(request.Secret))
	key.Write([]byte("codex2api:dispatch-diagnostic:v1"))
	block, err := aes.NewCipher(key.Sum(nil))
	require.NoError(test, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(test, err)
	nonce := make([]byte, aead.NonceSize())
	aad := strings.Join([]string{"codex2api:dispatch-diagnostic:v1", request.RequestID, strconv.Itoa(request.UserID), request.PlatformID}, "\n")
	return "v1." + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plaintext, []byte(aad)))
}

func TestCodexDispatchDiagnosticAuthenticationAndFixture(test *testing.T) {
	fixture, err := os.ReadFile("testdata/dispatch_diagnostic_v1.txt")
	require.NoError(test, err)
	encoded := strings.TrimSpace(string(fixture))
	request := newAPIPolicyRequestContext{RequestID: "dispatch-fixture", ChannelID: 7, UserID: 42, PlatformID: "test-platform", Secret: "integration-secret"}
	now := time.Unix(1788566400, 0)
	diagnostic, err := decodeCodexDispatchDiagnostic(encoded, request, now)
	require.NoError(test, err)
	assert.Equal(test, "root_owner_unavailable", diagnostic.Reason)
	assert.EqualValues(test, 42, diagnostic.RootAccount)
	assert.Equal(test, []string{"affinity_group_mismatch"}, diagnostic.Reasons)
	for _, field := range []string{"secret", "request", "user", "platform", "channel"} {
		test.Run(field, func(test *testing.T) {
			wrong := request
			switch field {
			case "secret":
				wrong.Secret = "different"
			case "request":
				wrong.RequestID = "another-request"
			case "user":
				wrong.UserID++
			case "platform":
				wrong.PlatformID = "another-platform"
			case "channel":
				wrong.ChannelID++
			}
			_, err := decodeCodexDispatchDiagnostic(encoded, wrong, now)
			require.Error(test, err)
		})
	}
	for _, invalid := range []string{"v2." + encoded[3:], encoded[:200], strings.Repeat("x", 3001), encoded[:150] + "!" + encoded[151:]} {
		_, err := decodeCodexDispatchDiagnostic(invalid, request, now)
		require.Error(test, err)
	}
	for _, clock := range []time.Time{now.Add(61 * time.Second), now.Add(-11 * time.Second)} {
		_, err := decodeCodexDispatchDiagnostic(encoded, request, clock)
		require.Error(test, err)
	}
	diagnostic.Reasons = []string{"arbitrary private upstream text"}
	_, err = decodeCodexDispatchDiagnostic(dispatchDiagnosticTestEnvelope(test, request, diagnostic), request, now)
	require.Error(test, err)
}

func TestCodexDispatchTransportPrivacy(test *testing.T) {
	request := newAPIPolicyRequestContext{RequestID: "dispatch-privacy", ChannelID: 7, UserID: 42, PlatformID: "test-platform", Secret: "integration-secret"}
	diagnostic := common.CodexDispatchDiagnostic{RequestID: request.RequestID, ChannelID: request.ChannelID, Status: 503, IssuedAt: time.Now().Unix(), Stage: "account_selection", Reason: "affinity_group_mismatch", Reasons: []string{"affinity_group_mismatch"}, Retry: "stop"}
	encoded := dispatchDiagnosticTestEnvelope(test, request, diagnostic)
	public := `{"error":{"code":"service_unavailable","message":"Request temporarily unavailable"}}`
	for _, trusted := range []bool{false, true} {
		for _, protocol := range []string{"http", "sse", "ws"} {
			test.Run(protocol+strconv.FormatBool(trusted), func(test *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Set(common.RequestIdKey, request.RequestID)
				request := request
				request.DispatchAttempt = common.CodexDispatchAttemptForContext(ctx)
				upstream := httptest.NewRequest(http.MethodPost, "http://codex2api.example/v1/responses", nil)
				if trusted {
					upstream = upstream.WithContext(context.WithValue(upstream.Context(), newAPIPolicyRequestContextKey{}, request))
					ctx.Set(newAPIPolicyRequestContextGinKey, request)
				}
				var output []byte
				if protocol == "ws" {
					message := []byte(`{"type":"error","error":{"code":"service_unavailable","message":"Request temporarily unavailable","details":{"request_id":"dispatch-privacy","codex2api_dispatch":"` + encoded + `"}}}`)
					output = SanitizeCodexDispatchWebSocketMessage(ctx, message)
				} else {
					response := &http.Response{StatusCode: 503, Header: make(http.Header), Request: upstream, Body: io.NopCloser(strings.NewReader(public))}
					if protocol == "sse" {
						response.StatusCode = 200
						response.Header.Set("Content-Type", "text/event-stream")
						stream := ": ping\n\n: codex2api_dispatch " + encoded + "\r\n\r\ndata: " + public + "\n\n"
						response.Body = io.NopCloser(iotest.OneByteReader(strings.NewReader(stream)))
					} else {
						response.Header.Set(codexDispatchDiagnosticHeader, encoded)
					}
					assert.False(test, processNewAPIPolicyResponse(ctx, response), "operational diagnostic must not become a policy decision")
					assert.Empty(test, response.Header.Get(codexDispatchDiagnosticHeader))
					var err error
					output, err = io.ReadAll(response.Body)
					require.NoError(test, err)
					require.NoError(test, response.Body.Close())
					assert.NotEqual(test, "true", response.Header.Get("X-Codex2API-Policy-Violation"))
				}
				for _, hidden := range []string{"codex2api_dispatch", "affinity_group_mismatch", "integration-secret", encoded} {
					assert.NotContains(test, string(output), hidden)
				}
				assert.Contains(test, string(output), "service_unavailable")
				captured, ok := common.GetCodexDispatchDiagnostic(ctx, request.ChannelID, 503)
				assert.Equal(test, trusted, ok)
				if trusted {
					assert.Equal(test, diagnostic.Reason, captured.Reason)
					assert.Equal(test, protocol != "http", captured.Stream)
				}
			})
		}
	}
}

func TestCodexDispatchStreamIgnoresInvalidMetadataWithoutCorruptingData(test *testing.T) {
	largeData := "data: " + strings.Repeat("x", 20000) + "\n"
	for _, diagnosticLine := range []string{": codex2api_dispatch invalid\n", ": codex2api_dispatch " + strings.Repeat("x", 20000) + "\n", ": codex2api_dispatch invalid"} {
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(largeData + diagnosticLine))}
		assert.False(test, processNewAPIPolicyResponse(nil, response))
		output, err := io.ReadAll(response.Body)
		require.NoError(test, err)
		assert.Equal(test, largeData, string(output))
	}
	for _, message := range []string{`{"type":"error","error":{"details":{"codex2api_dispatch":"bad"}}}`, `{"type":"error","error":{"details":{"codex2api_dispatch":"` + strings.Repeat("x", 20000) + `"}}}`} {
		assert.NotContains(test, string(SanitizeCodexDispatchWebSocketMessage(nil, []byte(message))), "codex2api_dispatch")
	}
	success := []byte(`{"type":"response.output_text.delta","delta":"codex2api_dispatch ` + strings.Repeat("x", 20000) + `"}`)
	assert.Equal(test, success, SanitizeCodexDispatchWebSocketMessage(nil, success))
}

func TestCodexDispatchSuccessfulResponsesDoNotAcquireFailureDiagnostics(test *testing.T) {
	for _, stream := range []bool{false, true} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(common.RequestIdKey, "success")
		request := newAPIPolicyRequestContext{RequestID: "success", ChannelID: 7, UserID: 42, PlatformID: "test-platform", Secret: "integration-secret", DispatchAttempt: common.CodexDispatchAttemptForContext(ctx)}
		diagnostic := common.CodexDispatchDiagnostic{RequestID: "success", ChannelID: 7, Status: 503, IssuedAt: time.Now().Unix(), Stage: "account_selection", Reason: "account_disabled", Reasons: []string{"account_disabled"}, Retry: "stop"}
		encoded := dispatchDiagnosticTestEnvelope(test, request, diagnostic)
		upstream := httptest.NewRequest("POST", "http://codex2api.example/v1/responses", nil).WithContext(context.WithValue(context.Background(), newAPIPolicyRequestContextKey{}, request))
		success := `{"type":"response.completed","response":{"status":"completed"}}`
		response := &http.Response{StatusCode: 200, Request: upstream, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(success))}
		response.Header.Set(codexDispatchDiagnosticHeader, encoded)
		if stream {
			response.Header.Set("Content-Type", "text/event-stream")
			success = "data: " + success + "\n\n"
			response.Body = io.NopCloser(strings.NewReader(": codex2api_dispatch " + encoded + "\n" + success))
		}
		assert.False(test, processNewAPIPolicyResponse(ctx, response))
		output, err := io.ReadAll(response.Body)
		require.NoError(test, err)
		assert.Equal(test, success, string(output))
		_, recorded := common.GetCodexDispatchDiagnostic(ctx, 7, 503)
		assert.False(test, recorded)
	}
}

func TestCodexDispatchUnboundResponseCannotApplyPolicyDecision(test *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	response := &http.Response{StatusCode: 503, Header: signedPolicyDecisionHeader("", "unbound-request")}
	response.Header.Set(codexDispatchDiagnosticHeader, "untrusted")
	assert.False(test, processNewAPIPolicyResponseWithContext(ctx, response, newAPIPolicyRequestContext{RequestID: "unbound-request", UserID: 42}))
	assert.Empty(test, response.Header.Get(codexDispatchDiagnosticHeader))
}
