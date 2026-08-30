package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	ReferralCommissionFrozen    = "frozen"
	ReferralCommissionAvailable = "available"
)

type ReferralCommission struct {
	Id            int    `json:"id"`
	TopUpId       int    `json:"topup_id" gorm:"uniqueIndex"`
	TradeNo       string `json:"trade_no" gorm:"type:varchar(255);index"`
	InviterId     int    `json:"inviter_id" gorm:"index"`
	InviteeId     int    `json:"invitee_id" gorm:"index"`
	BaseQuota     int    `json:"base_quota"`
	RateBps       int    `json:"rate_bps"`
	RewardQuota   int    `json:"reward_quota"`
	Status        string `json:"status" gorm:"type:varchar(20);index"`
	AvailableAt   int64  `json:"available_at" gorm:"index"`
	CreateTime    int64  `json:"create_time" gorm:"index"`
	InviteeName   string `json:"invitee_name" gorm:"-:all"`
	PaymentMethod string `json:"payment_method" gorm:"-:all"`
}

type ReferralSummary struct {
	AffCode             string `json:"aff_code"`
	InvitedCount        int    `json:"invited_count"`
	QualifiedCount      int    `json:"qualified_count"`
	RateBps             int    `json:"rate_bps"`
	UsersPerTier        int    `json:"users_per_tier"`
	NextTierRemaining   int    `json:"next_tier_remaining"`
	MaxRateBps          int    `json:"max_rate_bps"`
	QualifiedTopUpQuota int    `json:"qualified_topup_quota"`
	FrozenQuota         int    `json:"frozen_quota"`
	AvailableQuota      int    `json:"available_quota"`
	HistoryQuota        int    `json:"history_quota"`
}

// applyReferralCommissionWithTx records a commission in the same transaction
// that credits the top-up. Redemption codes and other balance adjustments do
// not call this function and therefore never contribute to referral totals.
func applyReferralCommissionWithTx(tx *gorm.DB, topUp *TopUp, creditedQuota int) error {
	if creditedQuota <= 0 || !operation_setting.IsPaymentComplianceConfirmed() {
		return nil
	}

	var invitee User
	if err := lockForUpdate(tx).First(&invitee, topUp.UserId).Error; err != nil {
		return err
	}
	// Existing invite relationships predate this commission program and are
	// intentionally excluded. Only registrations marked eligible at creation
	// can contribute qualified users or commissions.
	if !invitee.ReferralEligible || invitee.InviterId <= 0 || invitee.InviterId == invitee.Id {
		return nil
	}

	var inviter User
	if err := lockForUpdate(tx).First(&inviter, invitee.InviterId).Error; err != nil {
		return err
	}

	previousTopUpQuota := invitee.ReferralTopUpQuota
	invitee.ReferralTopUpQuota += int64(creditedQuota)
	qualifiedThreshold := common.QuotaFromFloat(float64(common.ReferralQualifiedTopUpUnits) * common.QuotaPerUnit)
	becameQualified := invitee.ReferralQualifiedAt == 0 && previousTopUpQuota < int64(qualifiedThreshold) && invitee.ReferralTopUpQuota >= int64(qualifiedThreshold)
	inviteeUpdates := map[string]interface{}{
		"referral_topup_quota": invitee.ReferralTopUpQuota,
	}
	if becameQualified {
		invitee.ReferralQualifiedAt = common.GetTimestamp()
		inviteeUpdates["referral_qualified_at"] = invitee.ReferralQualifiedAt
		inviter.AffQualifiedCount++
	}
	if err := tx.Model(&User{}).Where("id = ?", invitee.Id).Updates(inviteeUpdates).Error; err != nil {
		return err
	}

	rateBps := ReferralRateBps(inviter.AffQualifiedCount)
	rewardQuota := common.QuotaFromDecimal(
		decimal.NewFromInt(int64(creditedQuota)).
			Mul(decimal.NewFromInt(int64(rateBps))).
			Div(decimal.NewFromInt(10000)),
	)
	if rewardQuota <= 0 {
		if becameQualified {
			return tx.Model(&User{}).Where("id = ?", inviter.Id).
				Update("aff_qualified_count", inviter.AffQualifiedCount).Error
		}
		return nil
	}
	if inviter.AffFrozenQuota > common.MaxQuota-rewardQuota ||
		inviter.AffHistoryQuota > common.MaxQuota-rewardQuota ||
		inviter.AffCommissionQuota > common.MaxQuota-rewardQuota {
		return errors.New("referral commission quota exceeds the supported limit")
	}

	commission := ReferralCommission{
		TopUpId:     topUp.Id,
		TradeNo:     topUp.TradeNo,
		InviterId:   inviter.Id,
		InviteeId:   invitee.Id,
		BaseQuota:   creditedQuota,
		RateBps:     rateBps,
		RewardQuota: rewardQuota,
		Status:      ReferralCommissionFrozen,
		AvailableAt: time.Now().Add(common.ReferralFreezeHours * time.Hour).Unix(),
		CreateTime:  common.GetTimestamp(),
	}
	if err := tx.Create(&commission).Error; err != nil {
		return err
	}

	updates := map[string]interface{}{
		"aff_qualified_count":  inviter.AffQualifiedCount,
		"aff_frozen_quota":     gorm.Expr("aff_frozen_quota + ?", rewardQuota),
		"aff_history":          gorm.Expr("aff_history + ?", rewardQuota),
		"aff_commission_quota": gorm.Expr("aff_commission_quota + ?", rewardQuota),
	}
	return tx.Model(&User{}).Where("id = ?", inviter.Id).Updates(updates).Error
}

func ReleaseMaturedReferralCommissions(userId int) error {
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userId).Error; err != nil {
			return err
		}
		var rewardQuota int64
		if err := tx.Model(&ReferralCommission{}).
			Where("inviter_id = ? AND status = ? AND available_at <= ?", userId, ReferralCommissionFrozen, common.GetTimestamp()).
			Select("COALESCE(SUM(reward_quota), 0)").Scan(&rewardQuota).Error; err != nil {
			return err
		}
		if rewardQuota <= 0 {
			return nil
		}
		if rewardQuota > int64(common.MaxQuota-user.AffQuota) {
			return errors.New("available referral quota exceeds the supported limit")
		}
		if err := tx.Model(&ReferralCommission{}).
			Where("inviter_id = ? AND status = ? AND available_at <= ?", userId, ReferralCommissionFrozen, common.GetTimestamp()).
			Update("status", ReferralCommissionAvailable).Error; err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", userId).Updates(map[string]interface{}{
			"aff_frozen_quota": gorm.Expr("aff_frozen_quota - ?", rewardQuota),
			"aff_quota":        gorm.Expr("aff_quota + ?", rewardQuota),
		}).Error
	})
}

func GetReferralSummary(userId int) (*ReferralSummary, error) {
	if err := ReleaseMaturedReferralCommissions(userId); err != nil {
		return nil, err
	}
	user, err := GetUserById(userId, true)
	if err != nil {
		return nil, err
	}
	var commissionHistory int64
	if err := DB.Model(&ReferralCommission{}).Where("inviter_id = ?", userId).
		Select("COALESCE(SUM(reward_quota), 0)").Scan(&commissionHistory).Error; err != nil {
		return nil, err
	}
	rateBps := ReferralRateBps(user.AffQualifiedCount)
	usersPerTier := common.ReferralUsersPerTier
	if usersPerTier < 1 {
		usersPerTier = 1
	}
	nextTierRemaining := 0
	if rateBps < common.ReferralMaxRateBps {
		nextTierRemaining = usersPerTier - user.AffQualifiedCount%usersPerTier
	}
	return &ReferralSummary{
		AffCode:             user.AffCode,
		InvitedCount:        user.AffQualifiedCount,
		QualifiedCount:      user.AffQualifiedCount,
		RateBps:             rateBps,
		UsersPerTier:        usersPerTier,
		NextTierRemaining:   nextTierRemaining,
		MaxRateBps:          common.ReferralMaxRateBps,
		QualifiedTopUpQuota: common.QuotaFromFloat(float64(common.ReferralQualifiedTopUpUnits) * common.QuotaPerUnit),
		FrozenQuota:         user.AffFrozenQuota,
		AvailableQuota:      user.AffQuota,
		HistoryQuota:        common.QuotaFromDecimal(decimal.NewFromInt(commissionHistory)),
	}, nil
}

func GetReferralCommissions(userId int, pageInfo *common.PageInfo) ([]*ReferralCommission, int64, error) {
	if err := ReleaseMaturedReferralCommissions(userId); err != nil {
		return nil, 0, err
	}
	var commissions []*ReferralCommission
	var total int64
	query := DB.Model(&ReferralCommission{}).Where("referral_commissions.inviter_id = ?", userId)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.
		Select("referral_commissions.*, users.username AS invitee_name, top_ups.payment_method AS payment_method").
		Joins("LEFT JOIN users ON users.id = referral_commissions.invitee_id").
		Joins("LEFT JOIN top_ups ON top_ups.id = referral_commissions.top_up_id").
		Order("referral_commissions.id DESC").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Scan(&commissions).Error; err != nil {
		return nil, 0, err
	}
	return commissions, total, nil
}
