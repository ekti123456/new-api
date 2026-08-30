package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupReferralTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	previousBase := common.ReferralBaseRateBps
	previousPerTier := common.ReferralUsersPerTier
	previousMax := common.ReferralMaxRateBps
	previousQuotaPerUnit := common.QuotaPerUnit
	payment := operation_setting.GetPaymentSetting()
	previousConfirmed := payment.ComplianceConfirmed
	previousVersion := payment.ComplianceTermsVersion
	common.ReferralBaseRateBps = 500
	common.ReferralUsersPerTier = 5
	common.ReferralMaxRateBps = 1000
	common.QuotaPerUnit = 500000
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		common.ReferralBaseRateBps = previousBase
		common.ReferralUsersPerTier = previousPerTier
		common.ReferralMaxRateBps = previousMax
		common.QuotaPerUnit = previousQuotaPerUnit
		payment.ComplianceConfirmed = previousConfirmed
		payment.ComplianceTermsVersion = previousVersion
	})
}

func createReferralUser(t *testing.T, name string, inviterID int, eligible bool) User {
	t.Helper()
	user := User{
		Username: name, Password: "password-placeholder", AffCode: name,
		Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default",
		InviterId: inviterID, ReferralEligible: eligible,
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createEpayTopUp(t *testing.T, userID int, amount int64, suffix string) TopUp {
	t.Helper()
	topUp := TopUp{
		UserId: userID, Amount: amount, Money: float64(amount),
		TradeNo: fmt.Sprintf("referral-%s", suffix), PaymentMethod: "alipay",
		PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(&topUp).Error)
	return topUp
}

func TestReferralQualificationUsesCumulativePaidTopUps(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "inviter", 0, false)
	invitee := createReferralUser(t, "invitee", inviter.Id, true)

	first := createEpayTopUp(t, invitee.Id, 5, "first")
	require.NoError(t, RechargeEpay(first.TradeNo, "alipay", "127.0.0.1"))
	require.NoError(t, RechargeEpay(first.TradeNo, "alipay", "127.0.0.1"))
	require.NoError(t, DB.First(&invitee, invitee.Id).Error)
	assert.Equal(t, int64(5*500000), invitee.ReferralTopUpQuota)
	assert.Zero(t, invitee.ReferralQualifiedAt)
	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Zero(t, inviter.AffQualifiedCount)

	second := createEpayTopUp(t, invitee.Id, 5, "second")
	require.NoError(t, RechargeEpay(second.TradeNo, "alipay", "127.0.0.1"))
	require.NoError(t, DB.First(&invitee, invitee.Id).Error)
	assert.Equal(t, int64(10*500000), invitee.ReferralTopUpQuota)
	assert.NotZero(t, invitee.ReferralQualifiedAt)
	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Equal(t, 1, inviter.AffQualifiedCount)
	assert.Equal(t, 250000, inviter.AffFrozenQuota)
	assert.Equal(t, 250000, inviter.AffHistoryQuota)
	assert.Equal(t, 250000, inviter.AffCommissionQuota)

	var commissions []ReferralCommission
	require.NoError(t, DB.Order("id").Find(&commissions).Error)
	require.Len(t, commissions, 2)
	assert.Equal(t, 500, commissions[0].RateBps)
	assert.Equal(t, 125000, commissions[0].RewardQuota)
	assert.Equal(t, 500, commissions[1].RateBps)
}

func TestMaturedReferralCommissionBecomesTransferableOnce(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "release-inviter", 0, false)
	invitee := createReferralUser(t, "release-invitee", inviter.Id, true)
	topUp := createEpayTopUp(t, invitee.Id, 10, "release")
	require.NoError(t, RechargeEpay(topUp.TradeNo, "alipay", "127.0.0.1"))
	require.NoError(t, DB.Model(&ReferralCommission{}).Where("top_up_id = ?", topUp.Id).
		Update("available_at", common.GetTimestamp()-1).Error)

	require.NoError(t, ReleaseMaturedReferralCommissions(inviter.Id))
	require.NoError(t, ReleaseMaturedReferralCommissions(inviter.Id))
	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Zero(t, inviter.AffFrozenQuota)
	assert.Equal(t, 250000, inviter.AffQuota)
	var commission ReferralCommission
	require.NoError(t, DB.First(&commission).Error)
	assert.Equal(t, ReferralCommissionAvailable, commission.Status)
}

func TestLegacyInviteRelationshipDoesNotQualifyOrEarn(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "legacy-inviter", 0, false)
	invitee := createReferralUser(t, "legacy-invitee", inviter.Id, false)
	topUp := createEpayTopUp(t, invitee.Id, 10, "legacy")

	require.NoError(t, RechargeEpay(topUp.TradeNo, "alipay", "127.0.0.1"))
	require.NoError(t, DB.First(&invitee, invitee.Id).Error)
	assert.Zero(t, invitee.ReferralTopUpQuota)
	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Zero(t, inviter.AffQualifiedCount)
	assert.Zero(t, inviter.AffFrozenQuota)
	var count int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRedemptionDoesNotQualifyInviteeOrEarnCommission(t *testing.T) {
	setupReferralTest(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	t.Cleanup(func() { DB.Exec("DELETE FROM redemptions") })
	inviter := createReferralUser(t, "redeem-inviter", 0, false)
	invitee := createReferralUser(t, "redeem-invitee", inviter.Id, true)
	redemption := Redemption{
		Key: "referral-redemption-test-key", Status: common.RedemptionCodeStatusEnabled,
		Quota: 10 * 500000, CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(&redemption).Error)

	_, err := Redeem(redemption.Key, invitee.Id)
	require.NoError(t, err)
	require.NoError(t, DB.First(&invitee, invitee.Id).Error)
	assert.Zero(t, invitee.ReferralTopUpQuota)
	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Zero(t, inviter.AffQualifiedCount)
	assert.Zero(t, inviter.AffCommissionQuota)
	var count int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestReferralRateUsesNewTierOnQualifyingTopUp(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "tier-inviter", 0, false)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", inviter.Id).Update("aff_qualified_count", 4).Error)
	invitee := createReferralUser(t, "tier-invitee", inviter.Id, true)
	topUp := createEpayTopUp(t, invitee.Id, 10, "tier")

	require.NoError(t, RechargeEpay(topUp.TradeNo, "alipay", "127.0.0.1"))
	var commission ReferralCommission
	require.NoError(t, DB.First(&commission).Error)
	assert.Equal(t, 600, commission.RateBps)
	assert.Equal(t, 300000, commission.RewardQuota)
}

func TestReferralSummaryAggregatesReferredTopUpsAndCommissionCount(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "summary-inviter", 0, false)
	invitee := createReferralUser(t, "summary-invitee", inviter.Id, true)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", inviter.Id).Update("aff_count", 9).Error)

	first := createEpayTopUp(t, invitee.Id, 4, "summary-first")
	require.NoError(t, RechargeEpay(first.TradeNo, "alipay", "127.0.0.1"))
	second := createEpayTopUp(t, invitee.Id, 7, "summary-second")
	require.NoError(t, RechargeEpay(second.TradeNo, "wechat", "127.0.0.1"))

	summary, err := GetReferralSummary(inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(11*500000), summary.ReferredTopUpQuota)
	assert.Equal(t, int64(2), summary.CommissionCount)
	assert.Equal(t, 275000, summary.HistoryQuota)
	assert.Equal(t, 1, summary.InvitedCount)
	assert.Equal(t, 1, summary.QualifiedCount)
}

func TestGetReferralCommissionsFiltersAndPaginates(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "filter-inviter", 0, false)
	alice := createReferralUser(t, "alice_member", inviter.Id, true)
	bob := createReferralUser(t, "bob-member", inviter.Id, true)
	otherInviter := createReferralUser(t, "other-inviter", 0, false)
	otherInvitee := createReferralUser(t, "alice_member_other", otherInviter.Id, true)

	topUps := []TopUp{
		{UserId: alice.Id, TradeNo: "filter-alice-alipay", PaymentMethod: "alipay", Status: common.TopUpStatusSuccess},
		{UserId: alice.Id, TradeNo: "filter-alice-wechat", PaymentMethod: "wechat", Status: common.TopUpStatusSuccess},
		{UserId: bob.Id, TradeNo: "filter-bob-alipay", PaymentMethod: "alipay", Status: common.TopUpStatusSuccess},
		{UserId: otherInvitee.Id, TradeNo: "filter-other", PaymentMethod: "alipay", Status: common.TopUpStatusSuccess},
	}
	for index := range topUps {
		require.NoError(t, DB.Create(&topUps[index]).Error)
	}
	availableAt := common.GetTimestamp() + 10000
	commissions := []ReferralCommission{
		{TopUpId: topUps[0].Id, InviterId: inviter.Id, InviteeId: alice.Id, BaseQuota: 100, RewardQuota: 5, Status: ReferralCommissionFrozen, AvailableAt: availableAt, CreateTime: 100},
		{TopUpId: topUps[1].Id, InviterId: inviter.Id, InviteeId: alice.Id, BaseQuota: 200, RewardQuota: 10, Status: ReferralCommissionFrozen, AvailableAt: availableAt, CreateTime: 200},
		{TopUpId: topUps[2].Id, InviterId: inviter.Id, InviteeId: bob.Id, BaseQuota: 300, RewardQuota: 15, Status: ReferralCommissionFrozen, AvailableAt: availableAt, CreateTime: 300},
		{TopUpId: topUps[3].Id, InviterId: otherInviter.Id, InviteeId: otherInvitee.Id, BaseQuota: 400, RewardQuota: 20, Status: ReferralCommissionFrozen, AvailableAt: availableAt, CreateTime: 200},
	}
	require.NoError(t, DB.Create(&commissions).Error)

	pageInfo := &common.PageInfo{Page: 1, PageSize: 1}
	filter := ReferralCommissionFilter{
		Keyword:       "%alice_member%",
		PaymentMethod: "wechat",
		StartTime:     150,
		EndTime:       250,
	}
	results, total, err := GetReferralCommissions(inviter.Id, pageInfo, filter)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, results, 1)
	assert.Equal(t, commissions[1].Id, results[0].Id)
	assert.Equal(t, "alice_member", results[0].InviteeName)
	assert.Equal(t, "wechat", results[0].PaymentMethod)

	results, total, err = GetReferralCommissions(inviter.Id, &common.PageInfo{Page: 1, PageSize: 2}, ReferralCommissionFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, results, 2)
	assert.Equal(t, commissions[2].Id, results[0].Id)
	assert.Equal(t, commissions[1].Id, results[1].Id)
}

func TestGetReferralCommissionsRejectsUnsafeKeywordPattern(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "invalid-filter-inviter", 0, false)

	_, _, err := GetReferralCommissions(
		inviter.Id,
		&common.PageInfo{Page: 1, PageSize: 10},
		ReferralCommissionFilter{Keyword: "a%%%"},
	)
	require.Error(t, err)
}
