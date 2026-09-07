package service

import (
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/hashicorp/go-version"
)

var codexUserAgentVersionPattern = regexp.MustCompile(`(?i)^(Codex Desktop|codex-tui|codex_vscode|codex_cli_rs|codex_exec)(?:/([^ \t()]*))?(?:[ \t(]|$)`)
var codexClientSemverPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type UserAgentVersionRejection struct {
	Family         string
	CurrentVersion string
	MinimumVersion string
}

func CheckUserAgentVersionPolicy(userAgent string) *UserAgentVersionRejection {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	setting := operation_setting.GetUserAgentRoutingSetting()
	if !setting.VersionCheckEnabled {
		return nil
	}
	matched := codexUserAgentVersionPattern.FindStringSubmatch(strings.TrimSpace(userAgent))
	if len(matched) != 3 {
		return nil
	}
	family := matched[1]
	for _, name := range operation_setting.CodexUserAgentFamilies {
		if strings.EqualFold(family, name) {
			family = name
			break
		}
	}
	lower := strings.ToLower(userAgent)
	if family == "codex_cli_rs" || family == "codex_vscode" || family == "codex_exec" {
		if strings.Contains(lower, "(codex desktop;") {
			family = "Codex Desktop"
		} else if strings.Contains(lower, "(codex-tui;") {
			family = "codex-tui"
		}
	}
	minimum := strings.TrimSpace(setting.MinimumVersions[family])
	if minimum == "" {
		return nil
	}
	rejection := &UserAgentVersionRejection{Family: family, MinimumVersion: minimum}
	current := matched[2]
	if len(current) > 64 || !codexClientSemverPattern.MatchString(current) {
		return rejection
	}
	currentVersion, err := version.NewSemver(current)
	if err != nil {
		return rejection
	}
	rejection.CurrentVersion = current
	minimumVersion, err := version.NewSemver(minimum)
	if err != nil || currentVersion.LessThan(minimumVersion) {
		return rejection
	}
	return nil
}
