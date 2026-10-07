package service

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketUsesHTTP1ALPN(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			conn.Close()
		}
	}))
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if len(hello.SupportedProtos) != 1 || hello.SupportedProtos[0] != "http/1.1" {
			return nil, errors.New("HTTP/1.1 ALPN required")
		}
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	dialer, err := NewResponsesWebSocketDialer("")
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	dialer.TLSClientConfig.RootCAs = roots
	conn, response, err := dialer.DialContext(t.Context(), "wss"+strings.TrimPrefix(server.URL, "https"), nil)
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
}
