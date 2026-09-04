package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestCodexPassiveRootScopeSeparatesPlatformUserTokenAndInstallation(t *testing.T) {
	base := CodexPassiveRootScope{
		PlatformID: "newapi", UserID: 42, TokenID: 7, InstallationID: "device-a",
	}
	baseKey := codexPassiveRootRedisScopeKeyForScope(base)
	require.NotEmpty(t, baseKey)
	for name, other := range map[string]CodexPassiveRootScope{
		"platform":     {PlatformID: "other", UserID: 42, TokenID: 7, InstallationID: "device-a"},
		"user":         {PlatformID: "newapi", UserID: 43, TokenID: 7, InstallationID: "device-a"},
		"token":        {PlatformID: "newapi", UserID: 42, TokenID: 8, InstallationID: "device-a"},
		"installation": {PlatformID: "newapi", UserID: 42, TokenID: 7, InstallationID: "device-b"},
	} {
		require.NotEqual(t, baseKey, codexPassiveRootRedisScopeKeyForScope(other), "%s scope unexpectedly shared key", name)
	}
}

func TestCodexUnlinkedAccountFallbackSettingsClamp(t *testing.T) {
	previous := commonOptionMapSnapshot(t)
	t.Cleanup(func() { restoreCommonOptionMap(previous) })

	setCommonOption(CodexUnlinkedAccountFallbackEnabledOptionKey, "true")
	setCommonOption(CodexUnlinkedAccountFallbackSecondsOptionKey, "99999")
	require.True(t, CodexUnlinkedAccountFallbackEnabled())
	require.Equal(t, CodexUnlinkedAccountFallbackMaxSeconds, CodexUnlinkedAccountFallbackSeconds())
}

type optionSnapshot map[string]string

func commonOptionMapSnapshot(_ *testing.T) optionSnapshot {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	snapshot := make(optionSnapshot, 2)
	for _, key := range []string{CodexUnlinkedAccountFallbackEnabledOptionKey, CodexUnlinkedAccountFallbackSecondsOptionKey} {
		if value, found := common.OptionMap[key]; found {
			snapshot[key] = value
		}
	}
	return snapshot
}

func setCommonOption(key, value string) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[key] = value
	common.OptionMapRWMutex.Unlock()
}

func restoreCommonOptionMap(snapshot optionSnapshot) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	for _, key := range []string{CodexUnlinkedAccountFallbackEnabledOptionKey, CodexUnlinkedAccountFallbackSecondsOptionKey} {
		if value, found := snapshot[key]; found {
			common.OptionMap[key] = value
		} else {
			delete(common.OptionMap, key)
		}
	}
}
