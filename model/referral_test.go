package model

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

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

func TestInitializeLegacyReferralsBackfillsQualificationWithoutRewards(t *testing.T) {
	setupReferralTest(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Delete(&Option{}).Error)
	t.Cleanup(func() { DB.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Delete(&Option{}) })

	inviter := createReferralUser(t, "migration-inviter", 0, false)
	legacy := createReferralUser(t, "migration-legacy", inviter.Id, false)
	otherInviter := createReferralUser(t, "migration-other-inviter", 0, false)
	otherLegacy := createReferralUser(t, "migration-other-legacy", otherInviter.Id, false)

	historical := []TopUp{
		{UserId: legacy.Id, Amount: 5, TradeNo: "USR1NOlegacy-paid-before-provider", Status: common.TopUpStatusSuccess},
		{UserId: legacy.Id, Amount: 5, Money: 5, TradeNo: "legacy-paid-stripe", PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess},
		{UserId: legacy.Id, Money: 100, TradeNo: "legacy-subscription", PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess},
		{UserId: legacy.Id, Amount: 100, TradeNo: "legacy-cancelled", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusFailed},
		{UserId: legacy.Id, Amount: 100, TradeNo: "legacy-balance", PaymentMethod: PaymentMethodBalance, PaymentProvider: PaymentProviderBalance, Status: common.TopUpStatusSuccess},
		{UserId: legacy.Id, Amount: 100, Money: 1, TradeNo: "ref_ambiguous_without_method", Status: common.TopUpStatusSuccess},
		{UserId: otherLegacy.Id, Amount: 4, TradeNo: "legacy-other-paid", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess},
	}
	require.NoError(t, DB.Create(&historical).Error)
	require.NoError(t, DB.Create(&SubscriptionOrder{
		UserId: legacy.Id, TradeNo: "legacy-subscription", PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusSuccess,
	}).Error)

	require.NoError(t, InitializeLegacyReferrals())
	require.NoError(t, InitializeLegacyReferrals())

	require.NoError(t, DB.First(&legacy, legacy.Id).Error)
	assert.True(t, legacy.ReferralEligible)
	assert.True(t, legacy.ReferralLegacy)
	assert.Equal(t, int64(10*500000), legacy.ReferralTopUpQuota)
	assert.NotZero(t, legacy.ReferralQualifiedAt)
	require.NoError(t, DB.First(&otherLegacy, otherLegacy.Id).Error)
	assert.Equal(t, int64(4*500000), otherLegacy.ReferralTopUpQuota)
	assert.Zero(t, otherLegacy.ReferralQualifiedAt)

	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Equal(t, 1, inviter.AffQualifiedCount)
	assert.Zero(t, inviter.AffFrozenQuota)
	assert.Zero(t, inviter.AffQuota)
	assert.Zero(t, inviter.AffHistoryQuota)
	assert.Zero(t, inviter.AffCommissionQuota)
	var commissionCount int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&commissionCount).Error)
	assert.Zero(t, commissionCount)

	summary, err := GetReferralSummary(inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.QualifiedCount)
	assert.Equal(t, 500, summary.RateBps)
	assert.Equal(t, int64(10*500000), summary.ReferredTopUpQuota)
	assert.Zero(t, summary.HistoryQuota)
	assert.Zero(t, summary.CommissionCount)
}

func TestInitializeLegacyReferralsIsSafeWhenCalledConcurrently(t *testing.T) {
	setupReferralTest(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Delete(&Option{}).Error)
	t.Cleanup(func() { DB.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Delete(&Option{}) })
	inviter := createReferralUser(t, "concurrent-inviter", 0, false)
	invitee := createReferralUser(t, "concurrent-invitee", inviter.Id, false)
	topUp := TopUp{UserId: invitee.Id, Amount: 10, TradeNo: "USR-concurrent", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess}
	require.NoError(t, DB.Create(&topUp).Error)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- InitializeLegacyReferrals()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(&invitee, invitee.Id).Error)
	assert.True(t, invitee.ReferralLegacy)
	assert.NotZero(t, invitee.ReferralQualifiedAt)
}

func TestHistoricalTopUpCreditedQuotaRejectsSaturatedValues(t *testing.T) {
	setupReferralTest(t)

	assert.Zero(t, historicalTopUpCreditedQuota(&TopUp{
		Amount:          1,
		Money:           math.MaxFloat64,
		PaymentProvider: PaymentProviderStripe,
	}))
	assert.Zero(t, historicalTopUpCreditedQuota(&TopUp{
		Amount:          math.MaxInt64,
		PaymentProvider: PaymentProviderEpay,
	}))
}

func TestLegacyReferralEarnsOnlyOnTopUpsCompletedAfterMigration(t *testing.T) {
	setupReferralTest(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Delete(&Option{}).Error)
	t.Cleanup(func() { DB.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Delete(&Option{}) })

	inviter := createReferralUser(t, "future-inviter", 0, false)
	legacy := createReferralUser(t, "future-legacy", inviter.Id, false)
	historical := TopUp{
		UserId: legacy.Id, Amount: 5, TradeNo: "future-historical", PaymentMethod: "alipay",
		PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(&historical).Error)
	require.NoError(t, InitializeLegacyReferrals())
	require.NoError(t, DB.First(&legacy, legacy.Id).Error)
	assert.Equal(t, int64(5*500000), legacy.ReferralTopUpQuota)
	assert.Zero(t, legacy.ReferralQualifiedAt)

	future := createEpayTopUp(t, legacy.Id, 5, "future-paid")
	require.NoError(t, RechargeEpay(future.TradeNo, "wechat", "127.0.0.1"))

	var commissions []ReferralCommission
	require.NoError(t, DB.Find(&commissions).Error)
	require.Len(t, commissions, 1)
	assert.Equal(t, future.Id, commissions[0].TopUpId)
	assert.Equal(t, 5*500000, commissions[0].BaseQuota)
	assert.Equal(t, 125000, commissions[0].RewardQuota)
	require.NoError(t, DB.First(&legacy, legacy.Id).Error)
	assert.Equal(t, int64(10*500000), legacy.ReferralTopUpQuota)
	require.NoError(t, DB.First(&inviter, inviter.Id).Error)
	assert.Equal(t, 1, inviter.AffQualifiedCount)
	assert.Equal(t, 125000, inviter.AffFrozenQuota)
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
	assert.Equal(t, "a**********r", results[0].InviteeName)
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

func TestGetReferralMembersFiltersPaginatesAndIsolatesInviters(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "member-inviter", 0, false)
	legacyQualified := createReferralUser(t, "alice-legacy", inviter.Id, true)
	pending := createReferralUser(t, "alice-pending", inviter.Id, true)
	currentQualified := createReferralUser(t, "bob-current", inviter.Id, true)
	otherInviter := createReferralUser(t, "member-other-inviter", 0, false)
	otherMember := createReferralUser(t, "alice-other", otherInviter.Id, true)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", legacyQualified.Id).Updates(map[string]interface{}{
		"referral_legacy":       true,
		"referral_topup_quota":  int64(12 * 500000),
		"referral_qualified_at": int64(100),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", pending.Id).Update("referral_topup_quota", int64(5*500000)).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", currentQualified.Id).Updates(map[string]interface{}{
		"referral_topup_quota":  int64(10 * 500000),
		"referral_qualified_at": int64(200),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", otherMember.Id).Updates(map[string]interface{}{
		"referral_legacy":       true,
		"referral_topup_quota":  int64(20 * 500000),
		"referral_qualified_at": int64(300),
	}).Error)

	members, total, err := GetReferralMembers(inviter.Id, &common.PageInfo{Page: 1, PageSize: 1}, ReferralMemberFilter{
		Keyword: "alice",
		Status:  ReferralMemberStatusQualified,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, members, 1)
	assert.Equal(t, legacyQualified.Id, members[0].Id)
	assert.Equal(t, "a**********y", members[0].Username)
	assert.Equal(t, int64(12*500000), members[0].ReferralTopUpQuota)
	assert.Equal(t, int64(100), members[0].ReferralQualifiedAt)
	assert.True(t, members[0].Legacy)

	members, total, err = GetReferralMembers(inviter.Id, &common.PageInfo{Page: 1, PageSize: 10}, ReferralMemberFilter{Status: ReferralMemberStatusPending})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, members, 1)
	assert.Equal(t, pending.Id, members[0].Id)
	assert.False(t, members[0].Legacy)
}

func TestMaskReferralMemberUsernameUsesUnicodeCharacters(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     string
	}{
		{name: "empty", username: "", want: ""},
		{name: "one character", username: "a", want: "*"},
		{name: "two characters", username: "ab", want: "a*"},
		{name: "three characters", username: "abc", want: "a*c"},
		{name: "long username", username: "alice-legacy", want: "a**********y"},
		{name: "unicode", username: "李小龙", want: "李*龙"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, maskReferralMemberUsername(test.username))
		})
	}
}

func TestGetReferralMembersSortsByCreatedAtAndTopUpQuotaDeterministically(t *testing.T) {
	setupReferralTest(t)
	inviter := createReferralUser(t, "member-sort-inviter", 0, false)
	oldestHighestTopUp := createReferralUser(t, "member-sort-oldest", inviter.Id, true)
	newerLowestTopUp := createReferralUser(t, "member-sort-newer-low", inviter.Id, true)
	newerMiddleTopUp := createReferralUser(t, "member-sort-newer-middle", inviter.Id, true)
	middleMiddleTopUp := createReferralUser(t, "member-sort-middle", inviter.Id, true)

	updates := []struct {
		id        int
		createdAt int64
		topUp     int64
	}{
		{id: oldestHighestTopUp.Id, createdAt: 100, topUp: 30},
		{id: newerLowestTopUp.Id, createdAt: 300, topUp: 10},
		{id: newerMiddleTopUp.Id, createdAt: 300, topUp: 20},
		{id: middleMiddleTopUp.Id, createdAt: 200, topUp: 20},
	}
	for _, update := range updates {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", update.id).Updates(map[string]interface{}{
			"created_at": update.createdAt, "referral_topup_quota": update.topUp,
		}).Error)
	}

	memberIDs := func(filter ReferralMemberFilter) []int {
		members, total, err := GetReferralMembers(
			inviter.Id,
			&common.PageInfo{Page: 1, PageSize: 10},
			filter,
		)
		require.NoError(t, err)
		assert.Equal(t, int64(4), total)
		ids := make([]int, 0, len(members))
		for _, member := range members {
			ids = append(ids, member.Id)
		}
		return ids
	}

	assert.Equal(t, []int{
		newerMiddleTopUp.Id,
		newerLowestTopUp.Id,
		middleMiddleTopUp.Id,
		oldestHighestTopUp.Id,
	}, memberIDs(ReferralMemberFilter{}))
	assert.Equal(t, []int{
		oldestHighestTopUp.Id,
		middleMiddleTopUp.Id,
		newerLowestTopUp.Id,
		newerMiddleTopUp.Id,
	}, memberIDs(ReferralMemberFilter{
		SortBy: ReferralMemberSortCreatedAt, SortOrder: ReferralMemberSortAscending,
	}))
	assert.Equal(t, []int{
		oldestHighestTopUp.Id,
		middleMiddleTopUp.Id,
		newerMiddleTopUp.Id,
		newerLowestTopUp.Id,
	}, memberIDs(ReferralMemberFilter{
		SortBy: ReferralMemberSortTopUpQuota, SortOrder: ReferralMemberSortDescending,
	}))
	assert.Equal(t, []int{
		newerLowestTopUp.Id,
		newerMiddleTopUp.Id,
		middleMiddleTopUp.Id,
		oldestHighestTopUp.Id,
	}, memberIDs(ReferralMemberFilter{
		SortBy: ReferralMemberSortTopUpQuota, SortOrder: ReferralMemberSortAscending,
	}))

	// Unknown values never reach an SQL identifier and safely use the default.
	assert.Equal(t, []int{
		newerMiddleTopUp.Id,
		newerLowestTopUp.Id,
		middleMiddleTopUp.Id,
		oldestHighestTopUp.Id,
	}, memberIDs(ReferralMemberFilter{SortBy: "created_at; DROP TABLE users", SortOrder: "sideways"}))
}

func TestGetAdminReferralRankingsAggregatesAllAndTodayInvitationCohort(t *testing.T) {
	setupReferralTest(t)
	now := time.Now().Unix()
	dayStart := now - 1_000
	dayEnd := now + 1_000
	inviterA := createReferralUser(t, "ranking-inviter-a", 0, false)
	inviterA.DisplayName = "Inviter A"
	require.NoError(t, DB.Model(&User{}).Where("id = ?", inviterA.Id).Update("display_name", inviterA.DisplayName).Error)
	inviterB := createReferralUser(t, "ranking-inviter-b", 0, false)

	legacyQualified := createReferralUser(t, "ranking-legacy-qualified", inviterA.Id, true)
	todayPending := createReferralUser(t, "ranking-today-pending", inviterA.Id, true)
	todayQualified := createReferralUser(t, "ranking-today-qualified", inviterA.Id, true)
	oldMemberB := createReferralUser(t, "ranking-old-member-b", inviterB.Id, true)
	todayMemberB := createReferralUser(t, "ranking-today-member-b", inviterB.Id, true)
	ineligible := createReferralUser(t, "ranking-ineligible", inviterA.Id, false)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", legacyQualified.Id).Updates(map[string]interface{}{
		// A migration-time qualification inside today's range must not turn a
		// historical invitation into a member of today's acquisition cohort.
		"created_at": dayStart - 900, "referral_qualified_at": dayStart + 200, "referral_topup_quota": int64(12_000_000),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", todayPending.Id).Updates(map[string]interface{}{
		"created_at": dayStart + 100, "referral_topup_quota": int64(2_000_000),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", todayQualified.Id).Updates(map[string]interface{}{
		"created_at": dayStart + 150, "referral_qualified_at": dayStart + 300, "referral_topup_quota": int64(10_000_000),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", oldMemberB.Id).Updates(map[string]interface{}{
		"created_at": dayStart - 700, "referral_topup_quota": int64(4_000_000),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", todayMemberB.Id).Updates(map[string]interface{}{
		"created_at": dayStart + 250, "referral_topup_quota": int64(3_000_000),
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", ineligible.Id).Updates(map[string]interface{}{
		"created_at": dayStart + 350, "referral_qualified_at": dayStart + 400, "referral_topup_quota": int64(99_000_000),
	}).Error)

	topUps := []TopUp{
		{UserId: todayQualified.Id, TradeNo: "ranking-today-a", Status: common.TopUpStatusSuccess},
		{UserId: todayMemberB.Id, TradeNo: "ranking-today-b", Status: common.TopUpStatusSuccess},
		{UserId: legacyQualified.Id, TradeNo: "ranking-old-a", Status: common.TopUpStatusSuccess},
		{UserId: oldMemberB.Id, TradeNo: "ranking-old-b", Status: common.TopUpStatusSuccess},
	}
	require.NoError(t, DB.Create(&topUps).Error)
	commissions := []ReferralCommission{
		{TopUpId: topUps[0].Id, InviterId: inviterA.Id, InviteeId: todayQualified.Id, RewardQuota: 100, Status: ReferralCommissionFrozen, CreateTime: dayStart + 400},
		{TopUpId: topUps[1].Id, InviterId: inviterB.Id, InviteeId: todayMemberB.Id, RewardQuota: 200, Status: ReferralCommissionFrozen, CreateTime: dayStart + 450},
		{TopUpId: topUps[2].Id, InviterId: inviterA.Id, InviteeId: legacyQualified.Id, RewardQuota: 50, Status: ReferralCommissionAvailable, CreateTime: dayStart - 500},
		// A historical invitee earning commission today must remain outside
		// today's new-invitation cohort.
		{TopUpId: topUps[3].Id, InviterId: inviterB.Id, InviteeId: oldMemberB.Id, RewardQuota: 700, Status: ReferralCommissionFrozen, CreateTime: dayStart + 500},
	}
	require.NoError(t, DB.Create(&commissions).Error)

	all, total, err := GetAdminReferralRankings(&common.PageInfo{Page: 1, PageSize: 10}, AdminReferralRankingFilter{Period: AdminReferralRankingPeriodAll})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, all, 2)
	assert.Equal(t, inviterA.Id, all[0].UserId)
	assert.Equal(t, "Inviter A", all[0].DisplayName)
	assert.Equal(t, int64(3), all[0].InvitedCount)
	assert.Equal(t, int64(2), all[0].QualifiedCount)
	assert.Equal(t, int64(24_000_000), all[0].TopUpQuota)
	assert.Equal(t, int64(150), all[0].CommissionQuota)
	assert.Equal(t, int64(2), all[0].CommissionCount)

	today, total, err := GetAdminReferralRankings(&common.PageInfo{Page: 1, PageSize: 1}, AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: dayStart, EndTime: dayEnd,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, today, 1)
	assert.Equal(t, inviterA.Id, today[0].UserId)
	assert.Equal(t, int64(2), today[0].InvitedCount)
	assert.Equal(t, int64(1), today[0].QualifiedCount)
	assert.Equal(t, int64(12_000_000), today[0].TopUpQuota)
	assert.Equal(t, int64(100), today[0].CommissionQuota)
	assert.Equal(t, int64(1), today[0].CommissionCount)

	today, total, err = GetAdminReferralRankings(&common.PageInfo{Page: 2, PageSize: 1}, AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: dayStart, EndTime: dayEnd,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, today, 1)
	assert.Equal(t, inviterB.Id, today[0].UserId)
	assert.Equal(t, int64(1), today[0].InvitedCount)
	assert.Zero(t, today[0].QualifiedCount)
	assert.Equal(t, int64(3_000_000), today[0].TopUpQuota)
	assert.Equal(t, int64(200), today[0].CommissionQuota)
	assert.Equal(t, int64(1), today[0].CommissionCount)
}

func TestResolveAdminReferralRankingRangeUsesLocalCalendarDayAndValidatesOverrides(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.August, 30, 18, 45, 0, 0, location)
	start, end, err := resolveAdminReferralRankingRange(AdminReferralRankingFilter{Period: AdminReferralRankingPeriodToday}, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, time.August, 30, 0, 0, 0, 0, location).Unix(), start)
	assert.Equal(t, time.Date(2026, time.August, 31, 0, 0, 0, 0, location).Unix(), end)

	explicitStart := now.Unix() - 60
	explicitEnd := now.Unix() + 60
	start, end, err = resolveAdminReferralRankingRange(AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: explicitStart, EndTime: explicitEnd,
	}, now)
	require.NoError(t, err)
	assert.Equal(t, explicitStart, start)
	assert.Equal(t, explicitEnd, end)

	_, _, err = resolveAdminReferralRankingRange(AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodAll, StartTime: 1, EndTime: 2,
	}, now)
	require.Error(t, err)
	_, _, err = resolveAdminReferralRankingRange(AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: 1,
	}, now)
	require.Error(t, err)
	_, _, err = resolveAdminReferralRankingRange(AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: 1, EndTime: 1 + 27*60*60,
	}, now)
	require.Error(t, err)
	_, _, err = resolveAdminReferralRankingRange(AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: now.Unix() - 120, EndTime: now.Unix() - 60,
	}, now)
	require.Error(t, err)
	_, _, err = resolveAdminReferralRankingRange(AdminReferralRankingFilter{
		Period: AdminReferralRankingPeriodToday, StartTime: now.Unix() + 60, EndTime: now.Unix() + 120,
	}, now)
	require.Error(t, err)

	_, _, err = GetAdminReferralRankings(
		&common.PageInfo{Page: 1, PageSize: -1},
		AdminReferralRankingFilter{Period: AdminReferralRankingPeriodAll},
	)
	require.Error(t, err)
}
