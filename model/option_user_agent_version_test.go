package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserAgentVersionOptionsValidateAndRestore(test *testing.T) {
	for _, value := range []string{`{}`, `{"Codex Desktop":"0.153.0"}`, `{"codex-tui":""}`} {
		assert.NoError(test, validateOptionValue("user_agent_routing_setting.minimum_versions", value), value)
	}
	for _, value := range []string{`null`, `[]`, `{"curl":"0.153.0"}`, `{"codex-tui":"0.153"}`, `{"codex-tui":"0.153.0-alpha"}`, `{"codex-tui":"-1.2.3"}`, `{"codex-tui":153}`, `{"codex-tui":"9999999999.0.0"}`} {
		assert.Error(test, validateOptionValue("user_agent_routing_setting.minimum_versions", value), value)
	}
	assert.Error(test, validateOptionValue("user_agent_routing_setting.version_check_enabled", "invalid"))
	setting := operation_setting.GetUserAgentRoutingSetting()
	original := *setting
	test.Cleanup(func() { *setting = original })
	options := map[string]string{
		"user_agent_routing_setting.version_check_enabled": "true",
		"user_agent_routing_setting.minimum_versions":      `{"Codex Desktop":"0.153.0"}`,
	}
	require.NoError(test, config.GlobalConfig.LoadFromDB(options))
	assert.True(test, setting.VersionCheckEnabled)
	assert.Equal(test, "0.153.0", setting.MinimumVersions["Codex Desktop"])
	options["user_agent_routing_setting.minimum_versions"] = `{}`
	require.NoError(test, config.GlobalConfig.LoadFromDB(options))
	assert.Empty(test, setting.MinimumVersions, "removed requirements must not survive a settings reload")
}
