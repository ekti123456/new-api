package channel

import (
	"errors"
	"net/url"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const windowConnectionContextKey = "window_authorized_connection"

// Request-local trusted state, never serialized, cached globally or accepted
// from a client. A hot reload affects new requests, not an authorized request's
// confirmation, same-window renewal or reservation cleanup.
type windowConnectionSnapshot struct {
	target               url.URL
	binding              newAPIPolicyBinding
	enforcement          newAPIPolicyEnforcementConfig
	userID, channelID    int
	apiKey               string
	grantID, fingerprint string
}

func windowControlTarget(info *relaycommon.RelayInfo) (*url.URL, error) {
	if info == nil || info.ChannelMeta == nil {
		return nil, errors.New("missing window destination")
	}
	target, err := url.Parse(strings.TrimRight(info.ChannelBaseUrl, "/"))
	if err != nil || target == nil || target.Host == "" {
		return nil, errors.New("invalid window destination")
	}
	target.Path = strings.TrimSuffix(strings.TrimRight(target.Path, "/"), "/v1") + "/v1/session-windows"
	target.RawPath, target.RawQuery, target.Fragment = "", "", ""
	return target, nil
}

func captureWindowConnection(info *relaycommon.RelayInfo) (*windowConnectionSnapshot, error) {
	target, err := windowControlTarget(info)
	if err != nil {
		return nil, err
	}
	config, err := loadNewAPIPolicyConfig()
	if err != nil || !config.Enabled {
		return nil, errors.New("signed Codex2API integration is unavailable")
	}
	binding, matched := matchNewAPIPolicyBinding(config.Bindings, target, info.ApiKey)
	if !matched {
		return nil, errors.New("window destination requires a verified Codex2API binding")
	}
	return &windowConnectionSnapshot{target: *target, binding: binding, enforcement: config.Enforcement, userID: info.UserId, channelID: info.ChannelId, apiKey: info.ApiKey}, nil
}

func (s *windowConnectionSnapshot) policy() newAPIPolicyConfig {
	return newAPIPolicyConfig{Enabled: true, Bindings: []newAPIPolicyBinding{s.binding}, Enforcement: s.enforcement}
}

func (s *windowConnectionSnapshot) validateDestination(info *relaycommon.RelayInfo) error {
	target, err := windowControlTarget(info)
	if err != nil {
		return err
	}
	if s.userID != info.UserId || s.channelID != info.ChannelId || s.apiKey != info.ApiKey || s.target.String() != target.String() {
		return errors.New("window authorization cannot change its user or destination")
	}
	return nil
}

func rememberWindowConnection(c *gin.Context, connection *windowConnectionSnapshot, grant *relaycommon.WindowBillingGrant) {
	snapshot := *connection
	snapshot.grantID, snapshot.fingerprint = grant.ID, grant.Fingerprint
	c.Set(windowConnectionContextKey, snapshot)
}

func authorizedWindowConnection(c *gin.Context, info *relaycommon.RelayInfo) (*windowConnectionSnapshot, error) {
	if c == nil || info == nil || info.WindowBilling == nil {
		return nil, nil
	}
	value, found := c.Get(windowConnectionContextKey)
	if !found {
		return nil, nil
	}
	snapshot, ok := value.(windowConnectionSnapshot)
	if !ok {
		return nil, errors.New("invalid window connection snapshot")
	}
	if err := snapshot.validateDestination(info); err != nil {
		return nil, err
	}
	grant := info.WindowBilling
	if snapshot.grantID != grant.ID || snapshot.fingerprint != grant.Fingerprint || windowBindingHash(snapshot.binding, snapshot.apiKey) != grant.BindingHash {
		return nil, errors.New("window authorization does not match its connection snapshot")
	}
	return &snapshot, nil
}
