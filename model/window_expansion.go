package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func UpdateUserWindowExpansion(userID int, enabled bool, ratio float64) error {
	var settingValue string
	err := DB.Transaction(func(transaction *gorm.DB) error {
		var user User
		if err := lockForUpdate(transaction).Select("id", "setting").First(&user, userID).Error; err != nil {
			return err
		}
		setting := user.GetSetting()
		setting.WindowExpansionEnabled = enabled
		if enabled {
			setting.WindowExpansionJoined = true
			setting.WindowExpansionAcceptedRatio = ratio
		}
		payload, err := common.Marshal(setting)
		if err != nil {
			return err
		}
		settingValue = string(payload)
		return transaction.Model(&User{}).Where("id = ?", userID).Update("setting", settingValue).Error
	})
	if err != nil {
		return err
	}
	return updateUserSettingCache(userID, settingValue)
}
