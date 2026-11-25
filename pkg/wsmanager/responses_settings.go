package wsmanager

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const ResponsesOption = "ResponsesWebSocketSettings"

// Zero limits preserve unlimited admission. Zero stream and message overrides
// inherit the existing relay environment settings.
type ResponsesSettings struct {
	HandshakeSeconds       int `json:"handshake_seconds"`
	ClientHandshakeSeconds int `json:"client_handshake_seconds"`
	MaxConnections         int `json:"max_connections"`
	MaxUserConnections     int `json:"max_user_connections"`
	MaxClientHandshakes    int `json:"max_client_handshakes"`
	MaxHandshakes          int `json:"max_handshakes"`
	MaxChannelConnections  int `json:"max_channel_connections"`
	FirstFrameSeconds      int `json:"first_frame_seconds"`
	IdleSeconds            int `json:"idle_seconds"`
	StreamSeconds          int `json:"stream_seconds"`
	WriteSeconds           int `json:"write_seconds"`
	ClientMessageMiB       int `json:"client_message_mib"`
	UpstreamMessageMiB     int `json:"upstream_message_mib"`
	ShutdownSeconds        int `json:"shutdown_seconds"`
}

func DefaultResponsesSettings() ResponsesSettings {
	return ResponsesSettings{HandshakeSeconds: 45, WriteSeconds: 30}
}

func ParseResponsesSettings(raw string) (ResponsesSettings, error) {
	v := DefaultResponsesSettings()
	var fields map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(raw, &fields); err != nil || fields == nil {
		return v, fmt.Errorf("Responses WebSocket settings must be a JSON object")
	}
	for _, f := range []struct {
		name     string
		target   *int
		min, max int
	}{
		{"handshake_seconds", &v.HandshakeSeconds, 1, 86400},
		{"client_handshake_seconds", &v.ClientHandshakeSeconds, 0, 86400},
		{"max_connections", &v.MaxConnections, 0, 1000000},
		{"max_user_connections", &v.MaxUserConnections, 0, 1000000},
		{"max_client_handshakes", &v.MaxClientHandshakes, 0, 1000000},
		{"max_handshakes", &v.MaxHandshakes, 0, 1000000},
		{"max_channel_connections", &v.MaxChannelConnections, 0, 1000000},
		{"first_frame_seconds", &v.FirstFrameSeconds, 0, 604800},
		{"idle_seconds", &v.IdleSeconds, 0, 604800},
		{"stream_seconds", &v.StreamSeconds, 0, 604800},
		{"write_seconds", &v.WriteSeconds, 1, 86400},
		{"client_message_mib", &v.ClientMessageMiB, 0, 2048},
		{"upstream_message_mib", &v.UpstreamMessageMiB, 0, 2048},
		{"shutdown_seconds", &v.ShutdownSeconds, 0, 86400},
	} {
		raw, present := fields[f.name]
		if !present {
			continue
		}
		if common.GetJsonType(raw) != "number" || common.Unmarshal(raw, f.target) != nil || *f.target < f.min || *f.target > f.max {
			return v, fmt.Errorf("%s must be an integer between %d and %d", f.name, f.min, f.max)
		}
	}
	return v, nil
}

// Re-arm only idle sessions when their deadlines change. Routine option sync
// and ordinary frames do not scan the connection table.
func (m *ResponsesManager) Configure(v ResponsesSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.settings
	m.settings = v
	firstChanged, idleChanged := old.FirstFrameSeconds != v.FirstFrameSeconds, old.IdleSeconds != v.IdleSeconds
	if firstChanged || idleChanged {
		for s := range m.sessions {
			if (firstChanged && s.phase == waitingFirst) || (idleChanged && s.phase == idle) {
				s.armIdleLocked()
			}
		}
	}
}

func (m *ResponsesManager) Settings() ResponsesSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (c ResponsesSettings) WriteTimeout() time.Duration {
	return time.Duration(c.WriteSeconds) * time.Second
}
