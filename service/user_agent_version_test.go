package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserAgentVersionRequirementsCompareClientVersions(test *testing.T) {
	setting := operation_setting.GetUserAgentRoutingSetting()
	original := *setting
	test.Cleanup(func() { *setting = original })
	*setting = operation_setting.UserAgentRoutingSetting{VersionCheckEnabled: true, MinimumVersions: map[string]string{"Codex Desktop": "0.153.0", "codex-tui": "0.153.0", "codex_vscode": "0.153.0", "codex_cli_rs": "0.200.0"}}
	for _, scenario := range []struct {
		ua      string
		blocked bool
	}{
		{"Codex Desktop/0.152.9 (Windows; x86_64)", true},
		{"Codex Desktop/0.153.0 (Windows; x86_64)", false},
		{"codex-tui/0.99.0", true},
		{"codex-tui/0.154.0", false},
		{"CODEX_VSCODE/0.153.0", false},
		{"codex_vscode/0.153.0-alpha.1", true},
		{"codex_vscode/0.153.0+build.1", false},
		{"codex_vscode", true},
		{"codex_vscode/", true},
		{"codex_vscode/unknown", true},
		{"codex_vscode/0.153", true},
		{"codex_cli_rs/0.153.0 (Codex Desktop; Windows; x86_64)", false},
		{"codex_cli_rs/0.152.0 (codex-tui; Linux; x86_64)", true},
		{"codex_cli_rs/0.154.0", true},
		{"codex_exec/0.100.0", false},
		{"curl/8.0 (Codex Desktop; test)", false},
		{"not-codex_vscode/0.100.0", false},
		{"", false},
	} {
		test.Run(scenario.ua, func(test *testing.T) {
			rejection := CheckUserAgentVersionPolicy(scenario.ua)
			assert.Equal(test, scenario.blocked, rejection != nil)
			if scenario.blocked {
				require.NotNil(test, rejection)
				assert.NotEmpty(test, rejection.Family)
				assert.NotEmpty(test, rejection.MinimumVersion)
			}
		})
	}
	setting.VersionCheckEnabled = false
	assert.Nil(test, CheckUserAgentVersionPolicy("Codex Desktop/0.100.0"))
}
