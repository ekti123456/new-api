package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func resetChannelRoutingModeTestTables(t *testing.T, memoryCacheEnabled bool) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = memoryCacheEnabled
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}))
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	InitChannelCache()
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
		require.NoError(t, DB.Exec("DELETE FROM channels").Error)
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		InitChannelCache()
	})
}

func seedChannelRoutingModeTestChannel(t *testing.T, id int, priority int64, uaRoutingOnly bool) {
	t.Helper()
	channel := &Channel{
		Id:            id,
		Name:          fmt.Sprintf("routing-mode-%d", id),
		Status:        common.ChannelStatusEnabled,
		Group:         "default",
		Models:        "gpt-5",
		Priority:      &priority,
		UARoutingOnly: uaRoutingOnly,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-5",
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
	}).Error)
}

func TestGetRandomSatisfiedChannelStrictlySeparatesUARoutingChannels(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			resetChannelRoutingModeTestTables(t, memoryCacheEnabled)
			seedChannelRoutingModeTestChannel(t, 9101, 1, false)
			seedChannelRoutingModeTestChannel(t, 9102, 99, true)
			if memoryCacheEnabled {
				InitChannelCache()
			}

			normal, err := GetRandomSatisfiedChannelWithRoutingMode("default", "gpt-5", 0, "/v1/responses", false)
			require.NoError(t, err)
			require.NotNil(t, normal)
			require.Equal(t, 9101, normal.Id)

			routed, err := GetRandomSatisfiedChannelWithRoutingMode("default", "gpt-5", 0, "/v1/responses", true)
			require.NoError(t, err)
			require.NotNil(t, routed)
			require.Equal(t, 9102, routed.Id)
		})
	}
}

func TestUpdateChannelUARoutingOnlyPersistsFalse(t *testing.T) {
	resetChannelRoutingModeTestTables(t, false)
	seedChannelRoutingModeTestChannel(t, 9201, 1, true)

	require.NoError(t, UpdateChannelUARoutingOnly(9201, false))
	channel, err := GetChannelById(9201, true)
	require.NoError(t, err)
	require.False(t, channel.UARoutingOnly)
}
