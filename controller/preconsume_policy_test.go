package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPreConsumePolicyTest(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Token{}))
	previousConfig := config.GlobalConfig.ExportAllConfigs()
	previousUnit, previousBatch := common.QuotaPerUnit, common.BatchUpdateEnabled
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	common.QuotaPerUnit, common.BatchUpdateEnabled = 500000, false
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(previousConfig))
		common.QuotaPerUnit, common.BatchUpdateEnabled = previousUnit, previousBatch
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	return db
}

func TestPreConsumePolicyOptionPersistence(t *testing.T) {
	db := setupPreConsumePolicyTest(t)
	for _, tc := range []struct {
		key   string
		value any
		valid bool
	}{
		{"pre_consume_multiplier", 0.1, true},
		{"pre_consume_multiplier", "0", false},
		{"pre_consume_multiplier", "-0.5", false},
		{"pre_consume_multiplier", "NaN", false},
		{"pre_consume_multiplier", "Inf", false},
		{"pre_consume_multiplier", "1e309", false},
		{"pre_consume_multiplier", "", false},
		{"trust_quota_usd", 0, true},
		{"trust_quota_usd", 10.5, true},
		{"trust_quota_usd", "-1", false},
		{"trust_quota_usd", "NaN", false},
		{"trust_quota_usd", "Inf", false},
	} {
		t.Run(fmt.Sprint(tc.key, "/", tc.value), func(t *testing.T) {
			body, err := common.Marshal(OptionUpdateRequest{Key: "quota_setting." + tc.key, Value: tc.value})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(string(body)))
			UpdateOption(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct{ Success bool }
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.valid, response.Success)
		})
	}
	var saved []model.Option
	require.NoError(t, db.Find(&saved).Error)
	stored := make(map[string]string)
	for _, option := range saved {
		stored[option.Key] = option.Value
	}
	assert.Equal(t, "0.1", stored["quota_setting.pre_consume_multiplier"])
	assert.Equal(t, "10.5", stored["quota_setting.trust_quota_usd"])
	operation_setting.GetQuotaSetting().PreConsumeMultiplier = 1
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 10
	require.NoError(t, config.GlobalConfig.LoadFromDB(stored))
	assert.Equal(t, 0.1, operation_setting.GetQuotaSetting().PreConsumeMultiplier)
	assert.Equal(t, 10.5, operation_setting.GetQuotaSetting().TrustQuotaUSD)
}

func TestPreConsumePolicyWalletSettlement(t *testing.T) {
	db := setupPreConsumePolicyTest(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"policy-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"policy-model":"tier(\"base\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))
	for i, tc := range []struct {
		name                  string
		threshold, multiplier float64
		wallet, token         int
		force, unlimited      bool
		wantHeld              int
	}{
		{"above threshold", 10, 1, 5500000, 5500000, false, false, 0},
		{"custom threshold", 20, 1, 5500000, 5500000, false, false, 1500},
		{"zero threshold disables bypass", 0, 1, 5500000, 5500000, false, false, 1500},
		{"wallet equals threshold", 10, 1, 5000000, 5500000, false, false, 1500},
		{"token equals threshold", 10, 1, 5500000, 5000000, false, false, 1500},
		{"fractional threshold", 10.5, 1, 5250001, 5250001, false, false, 0},
		{"unlimited token", 10, 1, 5500000, 0, false, true, 0},
		{"forced reservation", 10, 1, 5500000, 5500000, true, false, 1500},
		{"one tenth input cost", 0, 0.1, 100000, 100000, false, false, 150},
		{"fractional multiplier refunds excess", 0, 2.5, 100000, 100000, false, false, 3750},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, model.UpdateOption("quota_setting.trust_quota_usd", strconv.FormatFloat(tc.threshold, 'f', -1, 64)))
			require.NoError(t, model.UpdateOption("quota_setting.pre_consume_multiplier", strconv.FormatFloat(tc.multiplier, 'f', -1, 64)))
			user := model.User{Username: fmt.Sprintf("policy-user-%d", i), Quota: tc.wallet, Group: "default", AffCode: fmt.Sprintf("policy-aff-%d", i)}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("policy-token-%d", i), RemainQuota: tc.token, UnlimitedQuota: tc.unlimited}
			require.NoError(t, db.Create(&token).Error)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			ctx.Set("token_quota", tc.token)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: tc.unlimited, ForcePreConsume: tc.force, OriginModelName: "policy-model", UserGroup: "default", UsingGroup: "default", BillingRequestInput: &billingexpr.RequestInput{}}
			info.UserSetting.BillingPreference = "wallet_only"
			price, err := helper.ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 64000})
			require.NoError(t, err)
			require.Nil(t, service.PreConsumeBilling(ctx, price.QuotaToPreConsume, info))
			assert.Equal(t, tc.wantHeld, info.FinalPreConsumedQuota)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, tc.wallet-tc.wantHeld, user.Quota)
			if !tc.unlimited {
				require.NoError(t, db.First(&token, token.Id).Error)
				assert.Equal(t, tc.token-tc.wantHeld, token.RemainQuota)
			}
			_, actual, _ := service.TryTieredSettle(info, billingexpr.TokenParams{P: 1000, C: 100, Len: 1000})
			assert.Equal(t, 2250, actual)
			require.NoError(t, info.Billing.Settle(actual))
			require.NoError(t, info.Billing.Settle(actual))
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, tc.wallet-actual, user.Quota)
			if !tc.unlimited {
				require.NoError(t, db.First(&token, token.Id).Error)
				assert.Equal(t, tc.token-actual, token.RemainQuota)
			}
		})
	}
}

func TestPreConsumeMultiplierRejectsInvalidRuntimeAndOverflow(t *testing.T) {
	previous := config.GlobalConfig.ExportAllConfigs()
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(previous)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"policy-overflow":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"policy-overflow":"tier(\"base\", p * 3)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))
	for _, multiplier := range []float64{0, -0.5, math.NaN(), math.Inf(1), math.MaxFloat64} {
		operation_setting.GetQuotaSetting().PreConsumeMultiplier = multiplier
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		info := &relaycommon.RelayInfo{OriginModelName: "policy-overflow", UserGroup: "default", UsingGroup: "default", BillingRequestInput: &billingexpr.RequestInput{}}
		_, err := helper.ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.Error(t, err)
		assert.Nil(t, info.Billing)
	}
}

func TestClaudePreConsumeBaseUsesLocalInputEstimate(t *testing.T) {
	previousConfig := config.GlobalConfig.ExportAllConfigs()
	previousCount, previousUnit := constant.CountToken, common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(previousConfig))
		constant.CountToken, common.QuotaPerUnit = previousCount, previousUnit
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"quota_setting.pre_consume_multiplier": "0.1",
		"billing_setting.billing_mode":         `{"claude-policy-model":"tiered_expr"}`,
		"billing_setting.billing_expr":         `{"claude-policy-model":"tier(\"base\", p * 20 + c * 125 + cr * 2)"}`,
		"group_ratio_setting.group_ratio":      `{"default":1}`,
	}))
	var request dto.ClaudeRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"claude-policy-model","max_tokens":64000,"system":[{"type":"text","text":"Reply briefly.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"Explain what this function does."}]}`), &request))
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint("count_tokens=", enabled), func(t *testing.T) {
			constant.CountToken = enabled
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, request.Model)
			info := &relaycommon.RelayInfo{OriginModelName: request.Model, UserGroup: "default", UsingGroup: "default", BillingRequestInput: &billingexpr.RequestInput{}}
			meta := request.GetTokenCountMeta()
			tokens, err := service.EstimateRequestToken(ctx, meta, info)
			require.NoError(t, err)
			price, err := helper.ModelPriceHelper(ctx, info, tokens, meta)
			require.NoError(t, err)
			if enabled {
				assert.Positive(t, tokens)
				assert.Positive(t, price.QuotaToPreConsume)
			} else {
				assert.Zero(t, tokens)
				assert.Zero(t, price.QuotaToPreConsume)
			}
			assert.EqualValues(t, 64000, *request.MaxTokens, "the upstream output limit must not be changed")
			usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100}
			usage.PromptTokensDetails.CachedTokens = 99000
			params := service.BuildTieredTokenParams(usage, true, billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString))
			ok, actual, result := service.TryTieredSettle(info, params)
			require.True(t, ok)
			require.NotNil(t, result)
			assert.Equal(t, 115250, actual, "final Claude input, cache and output charges use upstream usage regardless of local estimation")
		})
	}
}
