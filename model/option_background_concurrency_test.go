package model

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBackgroundConcurrencyOptionPersistsAndReloads(test *testing.T) {
	originalDB, originalLimit := DB, setting.GetBackgroundUserConcurrencyLimit()
	common.OptionMapRWMutex.Lock()
	originalOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	test.Cleanup(func() {
		DB = originalDB
		require.NoError(test, setting.UpdateBackgroundUserConcurrencyLimit(strconv.Itoa(originalLimit)))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptions
		common.OptionMapRWMutex.Unlock()
	})
	var err error
	DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(test, err)
	sqlDB, err := DB.DB()
	require.NoError(test, err)
	test.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(test, DB.AutoMigrate(&Option{}))
	require.NoError(test, UpdateOption("BackgroundUserConcurrencyLimit", "7"))
	assert.Equal(test, 7, setting.GetBackgroundUserConcurrencyLimit())
	var stored Option
	require.NoError(test, DB.First(&stored, "key = ?", "BackgroundUserConcurrencyLimit").Error)
	assert.Equal(test, "7", stored.Value)
	require.NoError(test, setting.UpdateBackgroundUserConcurrencyLimit("5"))
	require.NoError(test, updateOptionMap(stored.Key, stored.Value))
	assert.Equal(test, 7, setting.GetBackgroundUserConcurrencyLimit())
	for _, value := range []string{"0", "-1", "100001", "1.5", "invalid"} {
		assert.Error(test, UpdateOption("BackgroundUserConcurrencyLimit", value))
		assert.Error(test, updateOptionMap("BackgroundUserConcurrencyLimit", value))
		assert.Equal(test, 7, setting.GetBackgroundUserConcurrencyLimit())
	}
	common.OptionMapRWMutex.RLock()
	assert.Equal(test, "7", common.OptionMap["BackgroundUserConcurrencyLimit"])
	common.OptionMapRWMutex.RUnlock()
}
