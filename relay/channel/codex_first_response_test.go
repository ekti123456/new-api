package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexFirstResponseSurvivesSignedHTTPTransportAndIsNotForwarded(t *testing.T) {
	key := "timing-test-key"
	digest := sha256.Sum256([]byte(key))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotEmpty(t, r.Header.Get("X-NewAPI-Signature"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(codexTimingHeader, "v1-loose")
		w.Header().Set(codexFirstResponseHeader, "800")
		w.Header().Set(codexAttemptFirstResponseHeader, "600")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(server.Close)
	configurePolicyTest(t, []newAPIPolicyBinding{{Enabled: true, Target: server.URL, CodexKeyFingerprint: hex.EncodeToString(digest[:]), Secret: "0123456789abcdef0123456789abcdef", PlatformID: "newapi"}})
	body := []byte(`{"model":"test-model","input":"hello","stream":true}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	info := &relaycommon.RelayInfo{StartTime: time.Now().Add(-time.Second), IsStream: true, RequestId: "req-transport", UserId: 1,
		RelayFormat: types.RelayFormatOpenAIResponses, FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: key, ChannelBaseUrl: server.URL}}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
	resp, err := DoRequest(c, req, info)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.NotNil(t, info.CodexUpstreamFirstResponse)
	assert.EqualValues(t, 800, info.CodexUpstreamFirstResponse.Milliseconds)
	assert.Empty(t, resp.Header.Get(codexTimingHeader))
	assert.True(t, info.FirstResponseTime.IsZero(), "header parsing must not mark a stream as started")
	content, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "data: {\"type\":\"response.completed\"}\n\n", string(content))
}

func TestCodexFirstResponseTrustedHeaderAndFallback(t *testing.T) {
	key := "timing-test-key"
	digest := sha256.Sum256([]byte(key))
	configurePolicyTest(t, []newAPIPolicyBinding{{Enabled: true, Target: "http://codex.local", CodexKeyFingerprint: hex.EncodeToString(digest[:]), Secret: "0123456789abcdef0123456789abcdef", PlatformID: "newapi"}})
	now := time.Now()
	for _, tc := range []struct {
		name, mode, ms, attempt string
		trusted, want           bool
	}{
		{"valid", "v1-loose", "800", "600", true, true},
		{"zero", "v1-loose", "0", "0", true, true},
		{"untrusted", "v1-loose", "800", "600", false, false},
		{"missing", "", "", "", true, false},
		{"version", "v2-loose", "800", "600", true, false},
		{"negative", "v1-loose", "-1", "0", true, false},
		{"invalid", "v1-loose", "NaN", "0", true, false},
		{"overflow", "v1-loose", "99999999999999999999", "0", true, false},
		{"future", "v1-loose", "15000", "600", true, false},
		{"attempt larger", "v1-loose", "800", "801", true, false},
		{"combined header", "v1-loose", "800, 900", "600", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{StartTime: now.Add(-12 * time.Second), FirstResponseTime: now, IsStream: true, RequestId: "req-1", UserId: 1, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: key}}
			info.CodexUpstreamFirstResponse = &relaycommon.UpstreamFirstResponseTiming{Milliseconds: 1} // previous attempt must be cleared
			req := httptest.NewRequest(http.MethodPost, "http://codex.local/v1/responses", nil)
			if tc.trusted {
				req = req.WithContext(context.WithValue(req.Context(), newAPIPolicyRequestContextKey{}, newAPIPolicyRequestContext{Secret: "test-secret", RequestID: "req-1", ChannelID: 7, UserID: 1}))
			}
			resp := &http.Response{Request: req, StatusCode: http.StatusOK, Header: make(http.Header)}
			resp.Header.Set(codexTimingHeader, tc.mode)
			resp.Header.Set(codexFirstResponseHeader, tc.ms)
			resp.Header.Set(codexAttemptFirstResponseHeader, tc.attempt)
			captureCodexFirstResponse(resp, info, now)
			if tc.want {
				require.NotNil(t, info.CodexUpstreamFirstResponse)
				assert.Equal(t, "codex2api", info.CodexUpstreamFirstResponse.Source)
			} else {
				assert.Nil(t, info.CodexUpstreamFirstResponse)
			}
			assert.Equal(t, now, info.FirstResponseTime, "observed first frame must not be overwritten")
			assert.Empty(t, resp.Header, "private timing headers must not reach clients")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			other := service.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 1)
			assert.Equal(t, float64(12000), other["frt"], "stored actual first frame must remain unchanged")
			_, reported := other["upstream_first_response"]
			assert.Equal(t, tc.want, reported)
		})
	}
}

func TestCodexFirstResponseRejectsScopeAndDuplicateHeaders(t *testing.T) {
	key := "timing-test-key"
	digest := sha256.Sum256([]byte(key))
	configurePolicyTest(t, []newAPIPolicyBinding{{Enabled: true, Target: "http://codex.local", CodexKeyFingerprint: hex.EncodeToString(digest[:]), Secret: "0123456789abcdef0123456789abcdef", PlatformID: "newapi"}})
	now := time.Now()
	for _, kind := range []string{"request", "channel", "user", "key", "redirect", "status", "duplicate", "nonstream", "header-only-spoof"} {
		t.Run(kind, func(t *testing.T) {
			info := &relaycommon.RelayInfo{StartTime: now.Add(-time.Second), IsStream: true, RequestId: "req-1", UserId: 1, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: key}}
			scope := newAPIPolicyRequestContext{Secret: "test-secret", RequestID: "req-1", ChannelID: 7, UserID: 1}
			req := httptest.NewRequest(http.MethodPost, "http://codex.local/v1/responses", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
			resp.Header.Set(codexTimingHeader, "v1-loose")
			resp.Header.Set(codexFirstResponseHeader, "800")
			resp.Header.Set(codexAttemptFirstResponseHeader, "600")
			switch kind {
			case "request":
				scope.RequestID = "previous-request"
			case "channel":
				scope.ChannelID = 8
			case "user":
				scope.UserID = 2
			case "key":
				info.ApiKey = "unbound-key"
			case "redirect":
				req.URL.Host = "untrusted.local"
			case "status":
				resp.StatusCode = http.StatusTooManyRequests
			case "duplicate":
				resp.Header.Add(codexFirstResponseHeader, "900")
			case "nonstream":
				info.IsStream = false
			case "header-only-spoof":
				scope.Secret = ""
				req.Header.Set("X-NewAPI-Signature", "untrusted")
			}
			resp.Request = req.WithContext(context.WithValue(req.Context(), newAPIPolicyRequestContextKey{}, scope))
			captureCodexFirstResponse(resp, info, now)
			assert.Nil(t, info.CodexUpstreamFirstResponse)
			assert.Empty(t, resp.Header)
		})
	}
}
