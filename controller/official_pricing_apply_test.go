package controller

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOfficialPricesApplyPersistsExpressionAndReportsDatabaseFailure(t *testing.T) {
	originalDB := model.DB
	originalLogDB, originalRedis := model.LOG_DB, common.RedisEnabled
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	originalOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	modeJSON, err := common.Marshal(billing_setting.GetBillingModeCopy())
	require.NoError(t, err)
	exprJSON, err := common.Marshal(billing_setting.GetBillingExprCopy())
	require.NoError(t, err)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{Id: 42, Username: "pricing-admin"}).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		// Restore the registered configuration and option map independently.
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptions
		common.OptionMapRWMutex.Unlock()
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.RedisEnabled = originalRedis
		_ = sqlDB.Close()
	})
	// Bulk writes restore in-memory settings before the database-failure check.
	expression := `len > 128000 ? tier("long", p * 30 + c * 40) : tier("base", p * 10 + c * 20)`
	expressions, err := common.Marshal(map[string]string{"qwen-test": expression})
	require.NoError(t, err)
	update := officialPricingUpdate{Before: map[string]string{"billing_setting.billing_expr": "{}", "billing_setting.billing_mode": "{}"}, Values: map[string]string{"billing_setting.billing_expr": string(expressions), "billing_setting.billing_mode": `{"qwen-test":"tiered_expr"}`}}
	body, err := common.Marshal(update)
	require.NoError(t, err)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Set("id", 42)
	ctx.Request = httptest.NewRequest("POST", "/api/ratio_sync/official-prices/apply", bytes.NewReader(body))
	ApplyOfficialPrices(ctx)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(writer.Body.Bytes(), &response))
	require.True(t, response.Success, writer.Body.String())
	assert.Equal(t, "tiered_expr", billing_setting.GetBillingMode("qwen-test"))
	stored, exists := billing_setting.GetBillingExpr("qwen-test")
	assert.True(t, exists)
	assert.Equal(t, expression, stored)
	var options []model.Option
	require.NoError(t, db.Find(&options).Error)
	assert.Len(t, options, 2)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{"billing_setting.billing_mode": string(modeJSON), "billing_setting.billing_expr": string(exprJSON)}))
	require.NoError(t, sqlDB.Close())
	// A failed transaction returns a failure instead of reporting a successful sync.
	failedBody, err := common.Marshal(officialPricingUpdate{Before: map[string]string{"ModelRatio": "{}"}, Values: map[string]string{"ModelRatio": `{"qwen-test":5}`}})
	require.NoError(t, err)
	writer = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest("POST", "/api/ratio_sync/official-prices/apply", bytes.NewReader(failedBody))
	ApplyOfficialPrices(ctx)
	require.NoError(t, common.Unmarshal(writer.Body.Bytes(), &response))
	assert.False(t, response.Success)
}
