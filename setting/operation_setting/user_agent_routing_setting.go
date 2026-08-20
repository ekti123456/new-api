package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// UserAgentRoutingSetting routes every request whose User-Agent is not on the
// allowlist into one explicit channel pool. User-level allowlisting is carried
// separately in the authenticated user cache.
type UserAgentRoutingSetting struct {
	Enabled            bool     `json:"enabled"`
	UserAgentWhitelist []string `json:"user_agent_whitelist"`
	ChannelIDs         []int    `json:"channel_ids"`
}

var userAgentRoutingSetting = UserAgentRoutingSetting{}

func init() {
	config.GlobalConfig.Register("user_agent_routing_setting", &userAgentRoutingSetting)
}

func GetUserAgentRoutingSetting() *UserAgentRoutingSetting {
	return &userAgentRoutingSetting
}
