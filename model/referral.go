package model

import (
	"errors"
	"strings"
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
	InviteeName   string `json:"invitee_name" gorm:"->;-:migration"`
	PaymentMethod string `json:"payment_method" gorm:"->;-:migration"`
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
	ReferredTopUpQuota  int64  `json:"referred_topup_quota"`
	CommissionCount     int64  `json:"commission_count"`
}

type ReferralCommissionFilter struct {
	Keyword       string
	PaymentMethod string
	StartTime     int64
	EndTime       int64
}

const (
	ReferralMemberStatusQualified = "qualified"
	ReferralMemberStatusPending   = "pending"
)

type ReferralMember struct {
	Id                  int    `json:"id" gorm:"column:id"`
	Username            string `json:"username" gorm:"column:username"`
	CreatedAt           int64  `json:"created_at" gorm:"column:created_at"`
	ReferralTopUpQuota  int64  `json:"referral_topup_quota" gorm:"column:referral_topup_quota"`
	ReferralQualifiedAt int64  `json:"referral_qualified_at" gorm:"column:referral_qualified_at"`
	Legacy              bool   `json:"legacy" gorm:"column:referral_legacy"`
}

type ReferralMemberFilter struct {
	Keyword string
	Status  string
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
	var commissionAggregate struct {
		HistoryQuota    int64
		CommissionCount int64
	}
	if err := DB.Model(&ReferralCommission{}).Where("inviter_id = ?", userId).
		Select(`COALESCE(SUM(reward_quota), 0) AS history_quota,
			COUNT(*) AS commission_count`).
		Scan(&commissionAggregate).Error; err != nil {
		return nil, err
	}
	var memberAggregate struct {
		ReferredTopUpQuota int64
	}
	if err := DB.Model(&User{}).
		Where("inviter_id = ? AND referral_eligible = ?", userId, true).
		Select("COALESCE(SUM(referral_topup_quota), 0) AS referred_top_up_quota").
		Scan(&memberAggregate).Error; err != nil {
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
		HistoryQuota:        common.QuotaFromDecimal(decimal.NewFromInt(commissionAggregate.HistoryQuota)),
		ReferredTopUpQuota:  memberAggregate.ReferredTopUpQuota,
		CommissionCount:     commissionAggregate.CommissionCount,
	}, nil
}

func GetReferralMembers(userId int, pageInfo *common.PageInfo, filter ReferralMemberFilter) ([]*ReferralMember, int64, error) {
	if userId <= 0 {
		return nil, 0, errors.New("invalid user id")
	}
	var members []*ReferralMember
	var total int64
	query := DB.Model(&User{}).
		Where("inviter_id = ? AND referral_eligible = ?", userId, true)
	if filter.Keyword != "" {
		pattern, err := sanitizeLikePattern(filter.Keyword)
		if err != nil {
			return nil, 0, err
		}
		if !strings.Contains(pattern, "%") && len([]rune(pattern)) >= 2 {
			pattern = "%" + pattern + "%"
		}
		query = query.Where("username LIKE ? ESCAPE '!'", pattern)
	}
	switch filter.Status {
	case ReferralMemberStatusQualified:
		query = query.Where("referral_qualified_at > 0")
	case ReferralMemberStatusPending:
		query = query.Where("referral_qualified_at = 0")
	case "":
	default:
		return nil, 0, errors.New("invalid referral member status")
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.
		Select("id, username, created_at, referral_topup_quota, referral_qualified_at, referral_legacy").
		Order("created_at DESC").Order("id DESC").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).
		Scan(&members).Error; err != nil {
		return nil, 0, err
	}
	return members, total, nil
}

func GetReferralCommissions(userId int, pageInfo *common.PageInfo, filter ReferralCommissionFilter) ([]*ReferralCommission, int64, error) {
	if err := ReleaseMaturedReferralCommissions(userId); err != nil {
		return nil, 0, err
	}
	var commissions []*ReferralCommission
	var total int64
	query := DB.Model(&ReferralCommission{}).
		Joins("LEFT JOIN users ON users.id = referral_commissions.invitee_id").
		Joins("LEFT JOIN top_ups ON top_ups.id = referral_commissions.top_up_id").
		Where("referral_commissions.inviter_id = ?", userId)
	if filter.Keyword != "" {
		pattern, err := sanitizeLikePattern(filter.Keyword)
		if err != nil {
			return nil, 0, err
		}
		query = query.Where("users.username LIKE ? ESCAPE '!'", pattern)
	}
	if filter.PaymentMethod != "" {
		query = query.Where("top_ups.payment_method = ?", filter.PaymentMethod)
	}
	if filter.StartTime > 0 {
		query = query.Where("referral_commissions.create_time >= ?", filter.StartTime)
	}
	if filter.EndTime > 0 {
		query = query.Where("referral_commissions.create_time <= ?", filter.EndTime)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.
		Select("referral_commissions.*, users.username AS invitee_name, top_ups.payment_method AS payment_method").
		Order("referral_commissions.id DESC").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Scan(&commissions).Error; err != nil {
		return nil, 0, err
	}
	return commissions, total, nil
}
