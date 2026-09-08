package model

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWindowExpansionPreferenceSurvivesOtherSettingsAndDisabling(test *testing.T) {
	setupUserSessionTest(test)
	createUserSessionTestUser(test, 984721, 1)
	require.NoError(test, UpdateUserWindowExpansion(984721, true, 1.5))
	require.NoError(test, UpdateUserSetting(984721, dto.UserSetting{Language: "en"}))
	setting, err := GetUserSetting(984721, true)
	require.NoError(test, err)
	require.True(test, setting.WindowExpansionEnabled)
	require.True(test, setting.WindowExpansionJoined)
	require.Equal(test, 1.5, setting.WindowExpansionAcceptedRatio)
	require.Equal(test, "en", setting.Language)
	require.NoError(test, UpdateUserWindowExpansion(984721, false, 2))
	setting, err = GetUserSetting(984721, true)
	require.NoError(test, err)
	require.False(test, setting.WindowExpansionEnabled)
	require.True(test, setting.WindowExpansionJoined)
	require.Equal(test, 1.5, setting.WindowExpansionAcceptedRatio)
}
