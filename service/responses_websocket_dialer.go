package service

import (
	"crypto/tls"
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
