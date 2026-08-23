package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPerfUserSamplesListActiveRollupsByGroup(t *testing.T) {
	truncateTables(t)
	samples := []PerfUserSample{
		{UserId: 1, Username: "alice", Group: "pro", CreatedAt: 80, LastSeenAt: 89, RequestCount: 20},
		{UserId: 1, Username: "alice", Group: "pro", CreatedAt: 90, LastSeenAt: 101, RequestCount: 10, ErrorCount: 1},
		{UserId: 1, Username: "alice", Group: "other", CreatedAt: 90, LastSeenAt: 102, RequestCount: 10},
		{UserId: 2, Username: "bob", Group: "pro", CreatedAt: 90, LastSeenAt: 103, RequestCount: 10},
	}
	require.NoError(t, CreatePerfUserSamples(samples))

	rows, err := ListPerfUserSamples(90, []string{"pro"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, 1, rows[0].UserId)
	require.Equal(t, int64(10), rows[0].RequestCount)
	require.Equal(t, 2, rows[1].UserId)
}
