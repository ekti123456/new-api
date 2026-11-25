package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	conn, response, err := DialResponsesWebSocket(t.Context(), dialer, "wss"+strings.TrimPrefix(server.URL, "https"), nil)
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
}

func TestResponsesWebSocketCanceledHandshakeReleasesTransport(t *testing.T) {
	for _, proxy := range []string{"", "http://local.invalid:3128"} {
		name, method := "upgrade", http.MethodGet
		if proxy != "" {
			name, method = "proxy_connect", http.MethodConnect
		}
		t.Run(name, func(t *testing.T) {
			d, err := NewResponsesWebSocketDialer(proxy)
			require.NoError(t, err)
			if proxy == "" {
				d.Proxy = nil
			}
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			require.NoError(t, peer.SetDeadline(time.Now().Add(3*time.Second)))
			d.NetDialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, _, err := DialResponsesWebSocket(ctx, d, "ws://localhost/test", nil); result <- err }()
			req, err := http.ReadRequest(bufio.NewReader(peer))
			require.NoError(t, err)
			require.Equal(t, method, req.Method)
			cancel()
			var buf [1]byte
			_, err = peer.Read(buf[:])
			require.ErrorIs(t, err, io.EOF)
			select {
			case err := <-result:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(3 * time.Second):
				t.Fatal("canceled handshake did not return")
			}
		})
	}
}

func TestResponsesWebSocketEstablishedConnectionOutlivesDialContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		kind, data, err := ws.ReadMessage()
		if err == nil {
			_ = ws.WriteMessage(kind, data)
		}
	}))
	defer server.Close()
	d, err := NewResponsesWebSocketDialer("")
	require.NoError(t, err)
	d.Proxy = nil
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conn, _, err := DialResponsesWebSocket(ctx, d, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()
	cancel()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("next turn")))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "next turn", string(data))
}
