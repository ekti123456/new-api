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

func TestCodexProvisionalRootBindingDoesNotReplaceExistingDurableBinding(t *testing.T) {
	rootID := "root:" + t.Name()
	durable := CodexRootChannelBinding{ChannelID: 731, SelectedGroup: "pro", KeyFingerprint: "durable-key"}
	provisional := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "provisional-key"}
	require.NoError(t, StoreCodexRootChannelBinding(42, rootID, durable))
	require.NoError(t, StoreProvisionalCodexRootChannelBinding(42, rootID, provisional))

	got, found, err := LoadCodexRootChannelBinding(42, rootID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, durable, got)
}

func TestCodexRecentRootChannelBindingIsScopedByUserAndToken(t *testing.T) {
	rootID := "root:" + t.Name()
	binding := CodexRootChannelBinding{
		ChannelID:      812,
		SelectedGroup:  "pro",
		KeyIndex:       1,
		KeyFingerprint: "abcdef0123456789",
	}
	require.NoError(t, StoreRecentCodexRootChannelBinding(42, 101, rootID, binding))

	got, found, err := LoadRecentCodexRootChannelBinding(42, 101)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, rootID, got.RootID)
	require.Equal(t, binding, got.Binding)

	_, found, err = LoadRecentCodexRootChannelBinding(43, 101)
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = LoadRecentCodexRootChannelBinding(42, 102)
	require.NoError(t, err)
	require.False(t, found)
}

func TestCodexRecentRootChannelBindingRejectsMissingToken(t *testing.T) {
	rootID := "root:" + t.Name()
	require.NoError(t, StoreRecentCodexRootChannelBinding(42, 0, rootID, CodexRootChannelBinding{
		ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "abcdef0123456789",
	}))
	_, found, err := LoadRecentCodexRootChannelBinding(42, 0)
	require.NoError(t, err)
	require.False(t, found)
}
