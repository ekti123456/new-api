package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeUserAgentFamily(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		expected  string
	}{
		{
			name:      "codex desktop drops version and platform details",
			userAgent: "Codex Desktop/0.147.0-alpha.6.6 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.803.81509)",
			expected:  "Codex Desktop",
		},
		{
			name:      "codex tui drops version and terminal details",
			userAgent: "codex-tui/0.147.0 (Windows 10.0.26200; x86_64) WindowsTerminal (codex-tui; 0.147.0)",
			expected:  "codex-tui",
		},
		{name: "generic versioned client uses product family", userAgent: "curl/8.12.1", expected: "curl"},
		{name: "unversioned client keeps full name", userAgent: "custom desktop client", expected: "custom desktop client"},
		{name: "empty user agent is explicit", userAgent: "", expected: "Unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, normalizeUserAgentFamily(test.userAgent))
		})
	}
}

func TestUserAgentStatsAggregateVersionsAndSmallFamilies(t *testing.T) {
	truncateTables(t)
	userAgentStatCacheLock.Lock()
	userAgentStatCache = make(map[string]*UserAgentStat)
	userAgentStatCacheLock.Unlock()

	for i := 0; i < 80; i++ {
		LogUserAgentStat(1, "alice", 3700, "Codex Desktop/0.147.0 (Windows; x86_64)")
	}
	for i := 0; i < 15; i++ {
		LogUserAgentStat(2, "bob", 3700, "codex-tui/0.147.0 (Windows; x86_64)")
	}
	for i := 0; i < 3; i++ {
		LogUserAgentStat(1, "alice", 3700, "curl/8.12.1")
	}
	for i := 0; i < 2; i++ {
		LogUserAgentStat(1, "alice", 3700, "")
	}
	SaveUserAgentStatCache()

	stats, err := GetUserAgentStats(3600, 7200, "")
	require.NoError(t, err)
	require.Equal(t, int64(100), stats.Total)
	require.Equal(t, []UserAgentStatItem{
		{ClientFamily: "Codex Desktop", Count: 80, Percentage: 80},
		{ClientFamily: "codex-tui", Count: 15, Percentage: 15},
		{ClientFamily: "Other", Count: 5, Percentage: 5, IsOther: true},
	}, stats.Items)

	aliceStats, err := GetUserAgentStats(3600, 7200, "alice")
	require.NoError(t, err)
	require.Equal(t, int64(85), aliceStats.Total)
	require.Equal(t, "Codex Desktop", aliceStats.Items[0].ClientFamily)
	require.Equal(t, int64(80), aliceStats.Items[0].Count)
}
