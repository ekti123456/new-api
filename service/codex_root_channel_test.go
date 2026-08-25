package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexRootChannelBindingRoundtripIsScopedByUserAndRoot(t *testing.T) {
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{
		ChannelID:      731,
		SelectedGroup:  "pro",
		KeyIndex:       2,
		KeyFingerprint: "0123456789abcdef",
		UARoutingOnly:  true,
	}
	require.NoError(t, StoreCodexRootChannelBinding(42, rootID, binding))

	got, found, err := LoadCodexRootChannelBinding(42, rootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, binding, got)

	_, found, err = LoadCodexRootChannelBinding(43, rootID)
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = LoadCodexRootChannelBinding(42, rootID+"-other")
	require.NoError(t, err)
	require.False(t, found)
}

func TestCodexRootChannelBindingRejectsIncompleteValues(t *testing.T) {
	rootID := "root:" + t.Name()
	require.NoError(t, StoreCodexRootChannelBinding(42, rootID, CodexRootChannelBinding{ChannelID: 731}))
	_, found, err := LoadCodexRootChannelBinding(42, rootID)
	require.NoError(t, err)
	require.False(t, found)
}
