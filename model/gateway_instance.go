package model

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

// Nodes sharing one database share one identity; independent deployments do not.
// Persist before serving requests so a restart never changes continuation scope.
func EnsureGatewayInstanceID(ctx context.Context) error {
	option := Option{Key: common.GatewayInstanceIDOption, Value: uuid.NewString()}
	if err := DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&option).Error; err != nil {
		return fmt.Errorf("initialize gateway instance identity: %w", err)
	}
	if err := DB.WithContext(ctx).Where(&Option{Key: common.GatewayInstanceIDOption}).First(&option).Error; err != nil {
		return fmt.Errorf("load gateway instance identity: %w", err)
	}
	if _, err := uuid.Parse(strings.TrimSpace(option.Value)); err != nil {
		return fmt.Errorf("stored gateway instance identity is invalid")
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[common.GatewayInstanceIDOption] = strings.TrimSpace(option.Value)
	return nil
}
