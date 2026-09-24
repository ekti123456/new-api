package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const billingTestMetadata = `"codex2api_billing":{"version":1,"service_tier":"priority","source":"upstream_response","requested_service_tier":"","actual_service_tier":"priority","local_billing_service_tier":"default"}`

func TestCodexBillingSignedHTTPRoundTripAndRetryReset(t *testing.T) {
	const key = "billing-roundtrip-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Path == "/v1/responses" {
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{"+billingTestMetadata+"}}\n\n")
		} else {
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
		}
	}))
	t.Cleanup(server.Close)
	digest := sha256.Sum256([]byte(key))
	configurePolicyTest(t, []newAPIPolicyBinding{{Enabled: true, Target: server.URL, CodexKeyFingerprint: hex.EncodeToString(digest[:]), Secret: "0123456789abcdef0123456789abcdef", PlatformID: "newapi"}})
	info := &relaycommon.RelayInfo{RequestId: "billing-roundtrip", UserId: 1, IsStream: true,
		RelayFormat: types.RelayFormatOpenAIResponses, FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: key, ChannelBaseUrl: server.URL}}
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		body := []byte(`{"model":"test-model","input":"hello"}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
		require.NoError(t, err)
		require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
		resp, err := DoRequest(c, req, info)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		if path == "/v1/responses" {
			require.NotNil(t, info.CodexBilling.Snapshot())
			require.Equal(t, "priority", info.CodexBilling.Snapshot().ServiceTier)
		} else {
			require.Nil(t, info.CodexBilling.Snapshot(), "a new attempt cannot inherit the prior tier")
		}
	}
}

func configureBillingTest(t *testing.T) {
	t.Helper()
	digest := sha256.Sum256([]byte("billing-key"))
	configurePolicyTest(t, []newAPIPolicyBinding{{Enabled: true, Target: "http://codex.local", CodexKeyFingerprint: hex.EncodeToString(digest[:]), Secret: "0123456789abcdef0123456789abcdef", PlatformID: "newapi"}})
}

func billingTestResponse() (*http.Response, *relaycommon.RelayInfo) {
	info := &relaycommon.RelayInfo{RequestId: "req-billing", UserId: 1, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: "billing-key"}}
	req := httptest.NewRequest(http.MethodPost, "http://codex.local/v1/responses", nil)
	req = req.WithContext(context.WithValue(req.Context(), newAPIPolicyRequestContextKey{}, newAPIPolicyRequestContext{Secret: "test-secret", RequestID: info.RequestId, ChannelID: info.ChannelId, UserID: info.UserId}))
	return &http.Response{StatusCode: 200, Request: req, Header: make(http.Header)}, info
}

func TestCodexBillingObservationPreservesBytesWithModelLoggingDisabled(t *testing.T) {
	configureBillingTest(t)
	previous := common.UpstreamResponseModelLogEnabled.Load()
	t.Cleanup(func() { common.UpstreamResponseModelLogEnabled.Store(previous) })
	common.UpstreamResponseModelLogEnabled.Store(false)
	for _, tc := range []struct{ mime, body, protocol string }{
		{"application/json", `{"object":"response","status":"completed",` + billingTestMetadata + `}`, "codex2api_billing_v1"},
		{"text/event-stream", "data: {\"type\":\"response.created\",\"response\":{\"service_tier\":\"default\"}}\n\nevent: response.completed\ndata: {\"response\":{\"status\":\"completed\"," + billingTestMetadata + "}}\n\ndata: [DONE]\n\n", "codex2api_billing_v1"},
		{"application/json", `{"object":"response","service_tier":"priority"}`, "legacy_service_tier"},
		{"text/event-stream", "data: {\"object\":\"chat.completion.chunk\",\"choices\":[],\"service_tier\":\"priority\"}\n\ndata: [DONE]\n\n", "legacy_service_tier"},
	} {
		resp, info := billingTestResponse()
		resp.Header.Set("Content-Type", tc.mime)
		resp.Body = io.NopCloser(iotest.OneByteReader(strings.NewReader(tc.body)))
		observeResponseModelBody(resp, nil, newCodexBillingObserver(resp, info))
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, tc.body, string(out), "the observer must not rewrite the client response")
		report := info.CodexBilling.Snapshot()
		require.NotNil(t, report)
		require.Equal(t, "priority", report.ServiceTier)
		require.Equal(t, tc.protocol, report.Protocol)
		if tc.protocol == "codex2api_billing_v1" {
			require.Equal(t, "default", report.LocalBillingServiceTier)
		}
	}
}

func TestCodexBillingRejectsUntrustedScopeAndInvalidReports(t *testing.T) {
	configureBillingTest(t)
	for _, mismatch := range []string{"request", "user", "channel", "key", "destination", "unsigned", "error status"} {
		t.Run(mismatch, func(t *testing.T) {
			resp, info := billingTestResponse()
			switch mismatch {
			case "request":
				info.RequestId = "another-request"
			case "user":
				info.UserId++
			case "channel":
				info.ChannelId++
			case "key":
				info.ApiKey = "another-key"
			case "destination":
				resp.Request.URL.Host = "untrusted.invalid"
			case "unsigned":
				resp.Request = resp.Request.WithContext(context.Background())
			case "error status":
				resp.StatusCode = 400
			}
			require.Nil(t, newCodexBillingObserver(resp, info))
			require.Nil(t, info.CodexBilling.Snapshot())
		})
	}
	for _, body := range []string{
		`{"type":"response.created","response":{` + billingTestMetadata + `}}`,
		`{"type":"response.failed","response":{` + billingTestMetadata + `}}`,
		`{"object":"response","status":"failed",` + billingTestMetadata + `}`,
		`{"object":"response","error":{},` + billingTestMetadata + `}`,
		`{"object":"response",` + strings.Replace(billingTestMetadata, `"version":1`, `"version":1.1`, 1) + `}`,
		`{"object":"response",` + strings.Replace(billingTestMetadata, `"actual_service_tier":"priority"`, `"actual_service_tier":"default"`, 1) + `}`,
		`{"object":"response",` + strings.Replace(billingTestMetadata, `"upstream_response"`, `"invented_source"`, 1) + `}`,
		`{"tool_result":{"service_tier":"priority"}}`,
		`{"object":"response","service_tier":"private-unknown"}`,
		`{"object":"response","service_tier":"priority"`,
	} {
		resp, info := billingTestResponse()
		observer := newCodexBillingObserver(resp, info)
		require.NotNil(t, observer)
		observer([]byte(body), "")
		require.Nil(t, info.CodexBilling.Snapshot(), body)
	}
}

func TestCodexBillingOnlyCompleteBoundedEventsAreObserved(t *testing.T) {
	configureBillingTest(t)
	for _, following := range []bool{false, true} {
		resp, info := billingTestResponse()
		resp.Header.Set("Content-Type", "text/event-stream")
		body := "data: {\"type\":\"response.completed\",\"response\":{\"service_tier\":\"priority\",\"output\":\"" + strings.Repeat("x", responseModelObservationLimit) + "\"}}\n\n"
		if following {
			body += "data: {\"type\":\"response.completed\",\"response\":{" + billingTestMetadata + "}}\n\n"
		}
		resp.Body = io.NopCloser(strings.NewReader(body))
		observeResponseModelBody(resp, nil, newCodexBillingObserver(resp, info))
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(out))
		require.Equal(t, following, info.CodexBilling.Snapshot() != nil)
	}
}
