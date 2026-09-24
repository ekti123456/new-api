package common

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamTransportErrorClassificationPrivacyAndRetryScope(t *testing.T) {
	for _, tc := range []struct {
		code string
		err  error
	}{
		{"request_canceled", context.Canceled}, {"timeout", context.DeadlineExceeded},
		{"dns_error", &net.DNSError{Err: "no such host", Name: "private-host"}},
		{"connection_closed", io.EOF}, {"connection_closed", io.ErrUnexpectedEOF},
		{"connect_error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("private-proxy-password")}},
		{"tls_error", errors.New("tls: private-certificate-details")},
		{"transport_error", errors.New("unknown private failure")},
	} {
		t.Run(tc.code, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(RequestIdKey, "request-1")
			c.Set("channel_id", 7)
			req := httptest.NewRequest("POST", "https://example.com/responses?api_key=private-key", nil)
			RecordUpstreamTransportError(c, req, &url.Error{Op: "Post", URL: "https://user:private-password@private-host/?key=private-key", Err: tc.err}, 125*time.Millisecond)
			d, ok := GetUpstreamTransportError(c, 7)
			require.True(t, ok)
			require.Equal(t, tc.code, d.Code)
			require.EqualValues(t, 125, d.ElapsedMS)
			encoded, err := Marshal(d)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private")
			_, ok = GetUpstreamTransportError(c, 8)
			require.False(t, ok)
			ClearUpstreamTransportError(c)
			_, ok = GetUpstreamTransportError(c, 7)
			require.False(t, ok)
		})
	}
}
