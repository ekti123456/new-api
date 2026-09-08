package controller

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPersonalWindowResponseHidesInternalChannelIdentityWithoutChangingCache(test *testing.T) {
	pools := []personalWindowPool{{ID: 42, Name: "private@example.test", Available: true}}
	public := visiblePersonalWindowPools(pools, false)
	require.Equal(test, 1, public[0].ID)
	require.Equal(test, "Codex2API 1", public[0].Name)
	require.True(test, public[0].Available)
	admin := visiblePersonalWindowPools(pools, true)
	require.Equal(test, 42, admin[0].ID)
	require.Equal(test, "private@example.test", admin[0].Name)
}
