package service

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTieredChannelRetryReestimatesWithinSameGroup(test *testing.T) {
	expression := `channel_id == 12 && len > 272000 ? tier("long", p * 10) : tier("base", p * 5)`
	billing := &recordingBillingSettler{preConsumedQuota: 750000}
	info := &relaycommon.RelayInfo{
		UsingGroup: "external", Billing: billing, FinalPreConsumedQuota: 750000,
		ChannelMeta:         &relaycommon.ChannelMeta{ChannelId: 15},
		BillingRequestInput: &billingexpr.RequestInput{ChannelID: 15},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression),
			ChannelID: 15, GroupRatio: 1, QuotaPerUnit: 500000, EstimatedPromptTokens: 300000,
			EstimatedQuotaBeforeGroup: 750000, EstimatedQuotaAfterGroup: 750000, EstimatedTier: "base",
		},
		PriceData: types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}
	context, _ := gin.CreateTestContext(nil)
	context.Set("channel_id", 12)
	require.Nil(test, PrepareTieredBillingForSelectedGroup(context, info))
	assert.Equal(test, []int{1500000}, billing.reserveTargets)
	assert.Equal(test, "long", info.TieredBillingSnapshot.EstimatedTier)
	ok, quota, result := TryTieredSettle(info, billingexpr.TokenParams{P: 300000, Len: 300000})
	require.True(test, ok)
	require.NotNil(test, result)
	assert.Equal(test, 1500000, quota)
	assert.Equal(test, "long", result.MatchedTier)
	context.Set("channel_id", 15)
	info.ChannelMeta.ChannelId = 12
	require.Nil(test, PrepareTieredBillingForSelectedGroup(context, info))
	ok, quota, result = TryTieredSettle(info, billingexpr.TokenParams{P: 400000, Len: 400000})
	require.True(test, ok)
	require.NotNil(test, result)
	assert.Equal(test, 1000000, quota)
	assert.Equal(test, "base", result.MatchedTier)
	assert.Equal(test, 15, info.TieredBillingSnapshot.ChannelID)
}

func TestTieredChannelRetryKeepsFrozenPreConsumeMultiplier(t *testing.T) {
	previousMultiplier := operation_setting.GetQuotaSetting().PreConsumeMultiplier
	operation_setting.GetQuotaSetting().PreConsumeMultiplier = 10
	t.Cleanup(func() { operation_setting.GetQuotaSetting().PreConsumeMultiplier = previousMultiplier })
	expression := `channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 30) : tier("base", p * 5 + c * 15)`
	billing := &recordingBillingSettler{preConsumedQuota: 75000}
	info := &relaycommon.RelayInfo{
		UsingGroup: "external", Billing: billing, FinalPreConsumedQuota: 75000,
		ChannelMeta:         &relaycommon.ChannelMeta{ChannelId: 15},
		BillingRequestInput: &billingexpr.RequestInput{ChannelID: 15},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression),
			ChannelID: 15, GroupRatio: 1, QuotaPerUnit: 500000, EstimatedPromptTokens: 300000,
			PreConsumeMultiplier: 0.1, EstimatedQuotaBeforeGroup: 75000, EstimatedQuotaAfterGroup: 75000, EstimatedTier: "base",
		},
		PriceData: types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("channel_id", 12)
	require.Nil(t, PrepareTieredBillingForSelectedGroup(ctx, info))
	assert.Equal(t, []int{150000}, billing.reserveTargets)
	assert.Equal(t, "long", info.TieredBillingSnapshot.EstimatedTier)
	// A subsequent group/window change applies those factors once, without
	// multiplying the already-discounted reservation a second time.
	info.PriceData.GroupRatioInfo.GroupRatio = 2
	info.WindowBilling = &relaycommon.WindowBillingGrant{Expanded: true, Multiplier: 1.5}
	require.Nil(t, PrepareTieredBillingForSelectedGroup(ctx, info))
	assert.Equal(t, []int{150000, 450000}, billing.reserveTargets)
	ok, quota, result := TryTieredSettle(info, billingexpr.TokenParams{P: 300000, C: 100, Len: 300000})
	require.True(t, ok)
	require.NotNil(t, result)
	assert.Equal(t, 3003000, quota, "actual billing retains full prices and the group factor; window billing applies separately")
	assert.Equal(t, "long", result.MatchedTier)
}
