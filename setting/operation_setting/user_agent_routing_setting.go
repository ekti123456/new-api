package operation_setting

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// UserAgentRoutingSetting routes requests in selected channel groups whose
// User-Agent is not on the allowlist into one explicit channel pool.
type UserAgentRoutingSetting struct {
	Enabled             bool              `json:"enabled"`
	UserAgentWhitelist  []string          `json:"user_agent_whitelist"`
	ChannelIDs          []int             `json:"channel_ids"`
	GroupNames          []string          `json:"group_names"`
	VersionCheckEnabled bool              `json:"version_check_enabled"`
	MinimumVersions     map[string]string `json:"minimum_versions"`
}

var CodexUserAgentFamilies = [...]string{"Codex Desktop", "codex-tui", "codex_vscode", "codex_cli_rs", "codex_exec"}
var minimumClientVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

func ValidateUserAgentMinimumVersions(value string) error {
	var versions map[string]string
	if len(value) > 2048 || common.UnmarshalJsonStr(value, &versions) != nil || versions == nil {
		return fmt.Errorf("minimum_versions must be a JSON object")
	}
	for family, minimum := range versions {
		if !slices.Contains(CodexUserAgentFamilies[:], family) {
			return fmt.Errorf("unsupported User-Agent family: %s", family)
		}
		if minimum = strings.TrimSpace(minimum); minimum != "" && !minimumClientVersionPattern.MatchString(minimum) {
			return fmt.Errorf("%s minimum version must use major.minor.patch, for example 0.153.0", family)
		}
	}
	return nil
}

var userAgentRoutingSetting = UserAgentRoutingSetting{MinimumVersions: map[string]string{}}

func init() {
	config.GlobalConfig.Register("user_agent_routing_setting", &userAgentRoutingSetting)
}

func GetUserAgentRoutingSetting() *UserAgentRoutingSetting {
	return &userAgentRoutingSetting
}
