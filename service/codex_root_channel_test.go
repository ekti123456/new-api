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

func TestCodexRecentRootChannelBindingCorrelationSeparatesConcurrentPrompts(t *testing.T) {
	first := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "first-key"}
	second := CodexRootChannelBinding{ChannelID: 913, SelectedGroup: "pro", KeyFingerprint: "second-key"}
	require.NoError(t, StoreRecentCodexRootChannelBindingForCorrelation(42, 101, "prompt-a", "root-a", first))
	require.NoError(t, StoreRecentCodexRootChannelBindingForCorrelation(42, 101, "prompt-b", "root-b", second))

	got, found, err := LoadRecentCodexRootChannelBindingForCorrelation(42, 101, "prompt-a")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "root-a", got.RootID)
	require.Equal(t, first, got.Binding)
	got, found, err = LoadRecentCodexRootChannelBindingForCorrelation(42, 101, "prompt-b")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "root-b", got.RootID)
	require.Equal(t, second, got.Binding)
}

func TestCodexRecentRootChannelBindingCorrelationMarksDifferentRootsAmbiguous(t *testing.T) {
	first := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "pro", KeyFingerprint: "first-key"}
	second := CodexRootChannelBinding{ChannelID: 913, SelectedGroup: "pro", KeyFingerprint: "second-key"}
	require.NoError(t, StoreRecentCodexRootChannelBindingForCorrelation(43, 102, "same-prompt", "root-a", first))
	require.NoError(t, StoreRecentCodexRootChannelBindingForCorrelation(43, 102, "same-prompt", "root-b", second))

	got, found, err := LoadRecentCodexRootChannelBindingForCorrelation(43, 102, "same-prompt")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, got.Ambiguous)
	require.Empty(t, got.RootID)
	require.Zero(t, got.Binding.ChannelID)

	// A retry from either root cannot silently clear the collision and make the
	// result depend on arrival order.
	require.NoError(t, StoreRecentCodexRootChannelBindingForCorrelation(43, 102, "same-prompt", "root-a", first))
	got, found, err = LoadRecentCodexRootChannelBindingForCorrelation(43, 102, "same-prompt")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, got.Ambiguous)
}

func TestCodexRecentUserGroupChannelBindingIsScopedByUserAndGroup(t *testing.T) {
	binding := CodexRootChannelBinding{ChannelID: 812, SelectedGroup: "gpt-pro", KeyFingerprint: "abcdef0123456789"}
	require.NoError(t, StoreRecentCodexUserGroupChannelBinding(52, "gpt-pro", "", binding))

	got, found, err := LoadRecentCodexUserGroupChannelBinding(52, "gpt-pro")
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, got.RootID, "a rootless main turn must retain only its short channel bridge")
	require.Equal(t, binding, got.Binding)

	_, found, err = LoadRecentCodexUserGroupChannelBinding(53, "gpt-pro")
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = LoadRecentCodexUserGroupChannelBinding(52, "other")
	require.NoError(t, err)
	require.False(t, found)
}
