package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResponsesWebSocketOptionPersistsAndReloads(t *testing.T) {
	beforeDB, beforeCfg := DB, wsmanager.LocalResponses.Settings()
	key := wsmanager.ResponsesOption
	common.OptionMapRWMutex.Lock()
	beforeMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		DB = beforeDB
		wsmanager.LocalResponses.Configure(beforeCfg)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = beforeMap
		common.OptionMapRWMutex.Unlock()
	})
	var err error
	DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, DB.AutoMigrate(&Option{}))
	const config = `{"handshake_seconds":9,"first_frame_seconds":10,"idle_seconds":20,"max_connections":20}`
	require.NoError(t, UpdateOption(key, config))
	cfg := wsmanager.LocalResponses.Settings()
	assert.Equal(t, 20, cfg.MaxConnections)
	assert.Equal(t, 9, cfg.HandshakeSeconds)
	assert.Equal(t, 30, cfg.WriteSeconds)
	require.Error(t, UpdateOption(key, `{"handshake_seconds":null}`))
	var stored Option
	require.NoError(t, DB.Where(&Option{Key: key}).First(&stored).Error)
	require.Equal(t, config, stored.Value)
	wsmanager.LocalResponses.Configure(wsmanager.DefaultResponsesSettings())
	require.NoError(t, updateOptionMap(stored.Key, stored.Value))
	assert.Equal(t, cfg, wsmanager.LocalResponses.Settings())
	require.NoError(t, db.Close())
	require.Error(t, UpdateOption(key, `{"max_connections":1}`))
	assert.Equal(t, cfg, wsmanager.LocalResponses.Settings())
}
