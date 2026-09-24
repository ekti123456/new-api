package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const upstreamTransportErrorKey = "local_upstream_transport_error"

// Keep transport failures visible to administrators before the public error is
// generalized. Do not retain URLs, credentials, headers, bodies or raw errors.
type UpstreamTransportErrorDiagnostic struct {
	RequestID    string `json:"request_id"`
	ChannelID    int    `json:"channel_id"`
	Message      string `json:"message"`
	Code         string `json:"code"`
	Source       string `json:"source"`
	Stage        string `json:"stage"`
	Transport    string `json:"transport"`
	ErrorType    string `json:"error_type"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	RequestBytes int64  `json:"request_bytes"`
}

func RecordUpstreamTransportError(c *gin.Context, req *http.Request, err error, elapsed time.Duration) {
	if c == nil || err == nil {
		return
	}
	d := UpstreamTransportErrorDiagnostic{
		RequestID: c.GetString(RequestIdKey), ChannelID: c.GetInt("channel_id"),
		Code: "transport_error", Message: "Upstream HTTP transport failed", Source: "newapi_transport", Stage: "http_roundtrip", Transport: "http",
		ErrorType: fmt.Sprintf("%T", err), ElapsedMS: max(elapsed.Milliseconds(), 0), RequestBytes: -1,
	}
	if req != nil {
		d.RequestBytes = req.ContentLength
	}
	var dns *net.DNSError
	var op *net.OpError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		d.Code, d.Message = "request_canceled", "Request context canceled before response headers"
	case errors.Is(err, context.DeadlineExceeded):
		d.Code, d.Message = "timeout", "Upstream HTTP request timed out before response headers"
	case errors.As(err, &dns):
		d.Code, d.Message, d.Stage = "dns_error", "Upstream DNS resolution failed", "dns"
	case errors.As(err, &network) && network.Timeout():
		d.Code, d.Message = "timeout", "Upstream HTTP request timed out before response headers"
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		d.Code, d.Message = "connection_closed", "Connection closed before complete response headers"
	case strings.Contains(strings.ToLower(err.Error()), "tls:") || strings.Contains(strings.ToLower(err.Error()), "x509:"):
		d.Code, d.Message, d.Stage = "tls_error", "Upstream TLS handshake or certificate validation failed", "tls"
	}
	if errors.As(err, &op) {
		d.ErrorType = fmt.Sprintf("%T", op.Err)
		if d.Code == "transport_error" && op.Op == "dial" {
			d.Code, d.Message, d.Stage = "connect_error", "Upstream connection could not be established", "connect"
		}
	}
	c.Set(upstreamTransportErrorKey, d)
}

func ClearUpstreamTransportError(c *gin.Context) {
	if c != nil {
		c.Set(upstreamTransportErrorKey, nil)
	}
}

func GetUpstreamTransportError(c *gin.Context, channelID int) (UpstreamTransportErrorDiagnostic, bool) {
	if c == nil {
		return UpstreamTransportErrorDiagnostic{}, false
	}
	value, _ := c.Get(upstreamTransportErrorKey)
	d, ok := value.(UpstreamTransportErrorDiagnostic)
	return d, ok && d.ChannelID == channelID && d.RequestID == c.GetString(RequestIdKey)
}
