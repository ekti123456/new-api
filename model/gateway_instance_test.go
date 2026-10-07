package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGatewayInstanceIdentitySurvivesReloadAndDiffersAcrossDeployments(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	var original Option
	stored := DB.Where(&Option{Key: common.GatewayInstanceIDOption}).Find(&original).RowsAffected > 0
	require.NoError(t, DB.Where(&Option{Key: common.GatewayInstanceIDOption}).Delete(&Option{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where(&Option{Key: common.GatewayInstanceIDOption}).Delete(&Option{}).Error)
		if stored {
			require.NoError(t, DB.Create(&original).Error)
		}
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, EnsureGatewayInstanceID(t.Context()))
	first := common.GatewayInstanceID()
	_, err := uuid.Parse(first)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, EnsureGatewayInstanceID(t.Context()))
	require.Equal(t, first, common.GatewayInstanceID(), "restart must load the persisted ID")
	// A fresh deployment has no stored identity. It must not inherit a process
	// singleton even when a test process initializes another database state.
	require.NoError(t, DB.Where(&Option{Key: common.GatewayInstanceIDOption}).Delete(&Option{}).Error)
	require.NoError(t, EnsureGatewayInstanceID(t.Context()))
	require.NotEqual(t, first, common.GatewayInstanceID())
	// A corrupt stored ID must not silently rotate or collapse to an empty scope.
	require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: common.GatewayInstanceIDOption}).Update("value", "invalid").Error)
	require.Error(t, EnsureGatewayInstanceID(t.Context()))
}
