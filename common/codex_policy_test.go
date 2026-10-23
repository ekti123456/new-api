package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkSavedPolicyIPConfiguration(b *testing.B) {
	OptionMapRWMutex.Lock()
	previous := OptionMap
	OptionMap = map[string]string{CodexPolicyOption: `{"ip_block_enabled":true,"bindings":[{"secret":"` + strings.Repeat("x", 4096) + `"}]}`}
	OptionMapRWMutex.Unlock()
	b.Cleanup(func() { OptionMapRWMutex.Lock(); OptionMap = previous; OptionMapRWMutex.Unlock() })
	_, err := ReadCodexPolicyIPEnforcement()
	require.NoError(b, err)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		config, err := ReadCodexPolicyIPEnforcement()
		if err != nil || !config.Enabled {
			b.Fatal("saved settings not applied")
		}
	}
}
