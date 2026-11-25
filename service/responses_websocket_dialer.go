package service

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gorilla/websocket"
)

// NewResponsesWebSocketDialer uses the same proxy parser and remote-DNS SOCKS
// dialer as HTTP. An invalid or failed configured proxy never falls back direct.
func NewResponsesWebSocketDialer(rawProxy string) (*websocket.Dialer, error) {
	dialer := *websocket.DefaultDialer
	if dialer.TLSClientConfig == nil {
		dialer.TLSClientConfig = &tls.Config{}
	} else {
		dialer.TLSClientConfig = dialer.TLSClientConfig.Clone()
	}
	dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	if strings.TrimSpace(rawProxy) == "" {
		return &dialer, nil
	}
	proxyURL, _, err := common.ParseProxyURLRuntime(rawProxy)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	if err := configureProxyTransport(transport, proxyURL); err != nil {
		return nil, err
	}
	dialer.Proxy = transport.Proxy
	dialer.NetDialContext = transport.DialContext
	return &dialer, nil
}

// DialResponsesWebSocket ties cancellation to the physical connection only
// until the HTTP upgrade completes. Gorilla's deadline alone does not interrupt
// an outstanding CONNECT/101 read when the caller disconnects early.
func DialResponsesWebSocket(ctx context.Context, base *websocket.Dialer, target string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	dialer := *base
	var stop func() bool
	watch := func(conn net.Conn, err error) (net.Conn, error) {
		if err == nil {
			stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
		}
		return conn, err
	}
	netDial := dialer.NetDialContext
	if netDial == nil {
		netDial = (&net.Dialer{}).DialContext
		if legacy := dialer.NetDial; legacy != nil {
			netDial = func(_ context.Context, network, addr string) (net.Conn, error) {
				return legacy(network, addr)
			}
		}
	}
	dialer.NetDialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		return watch(netDial(dialCtx, network, addr))
	}
	if tlsDial := dialer.NetDialTLSContext; tlsDial != nil {
		dialer.NetDialTLSContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			return watch(tlsDial(dialCtx, network, addr))
		}
	}
	conn, resp, err := dialer.DialContext(ctx, target, headers)
	if stop != nil {
		stop()
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		if conn != nil {
			_ = conn.Close()
			return nil, nil, cancelErr
		}
		if resp == nil {
			return nil, nil, cancelErr
		}
	}
	return conn, resp, err
}
