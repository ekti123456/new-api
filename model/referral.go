package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	ReferralMemberSortCreatedAt   = "created_at"
	ReferralMemberSortTopUpQuota  = "topup_quota"
	ReferralMemberSortAscending   = "asc"
	ReferralMemberSortDescending  = "desc"
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
	Keyword   string
	Status    string
	SortBy    string
	SortOrder string
}

const (
	AdminReferralRankingPeriodAll   = "all"
	AdminReferralRankingPeriodToday = "today"
)

type AdminReferralRankingFilter struct {
	Period    string
	StartTime int64
	EndTime   int64
}

type AdminReferralRanking struct {
	UserId          int    `json:"user_id" gorm:"column:user_id"`
	Username        string `json:"username" gorm:"column:username"`
	DisplayName     string `json:"display_name" gorm:"column:display_name"`
	InvitedCount    int64  `json:"invited_count" gorm:"column:invited_count"`
	QualifiedCount  int64  `json:"qualified_count" gorm:"column:qualified_count"`
	TopUpQuota      int64  `json:"topup_quota" gorm:"column:topup_quota"`
	CommissionQuota int64  `json:"commission_quota" gorm:"column:commission_quota"`
	CommissionCount int64  `json:"commission_count" gorm:"column:commission_count"`
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
	sortBy, sortOrder := resolveReferralMemberSort(filter.SortBy, filter.SortOrder)
	sortColumn := "created_at"
	if sortBy == ReferralMemberSortTopUpQuota {
		sortColumn = "referral_topup_quota"
	}
	descending := sortOrder == ReferralMemberSortDescending
	if err := query.
		Select("id, username, created_at, referral_topup_quota, referral_qualified_at, referral_legacy").
		Order(clause.OrderByColumn{Column: clause.Column{Name: sortColumn}, Desc: descending}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: descending}).
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).
		Scan(&members).Error; err != nil {
		return nil, 0, err
	}
	for _, member := range members {
		member.Username = maskReferralMemberUsername(member.Username)
	}
	return members, total, nil
}

func maskReferralMemberUsername(username string) string {
	runes := []rune(username)
	switch len(runes) {
	case 0:
		return ""
	case 1:
		return "*"
	case 2:
		return string(runes[0]) + "*"
	default:
		return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
	}
}

func resolveReferralMemberSort(sortBy string, sortOrder string) (string, string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	switch sortBy {
	case ReferralMemberSortCreatedAt, ReferralMemberSortTopUpQuota:
	default:
		sortBy = ReferralMemberSortCreatedAt
	}

	sortOrder = strings.ToLower(strings.TrimSpace(sortOrder))
	if sortOrder != ReferralMemberSortAscending && sortOrder != ReferralMemberSortDescending {
		sortOrder = ReferralMemberSortDescending
	}
	return sortBy, sortOrder
}

func GetAdminReferralRankings(pageInfo *common.PageInfo, filter AdminReferralRankingFilter) ([]*AdminReferralRanking, int64, error) {
	if pageInfo == nil {
		return nil, 0, errors.New("page info is required")
	}
	if pageInfo.GetPage() < 1 || pageInfo.GetPageSize() < 1 || pageInfo.GetPageSize() > 100 {
		return nil, 0, errors.New("invalid referral ranking pagination")
	}
	startTime, endTime, err := resolveAdminReferralRankingRange(filter, time.Now())
	if err != nil {
		return nil, 0, err
	}

	memberStats := DB.Table("users AS invitees").
		Where("invitees.inviter_id > 0 AND invitees.inviter_id <> invitees.id AND invitees.referral_eligible = ? AND invitees.deleted_at IS NULL", true)
	commissionStats := DB.Table("referral_commissions AS ranked_commissions")

	const selectedTopUpQuota = "member_stats.topup_quota"
	switch filter.Period {
	case AdminReferralRankingPeriodAll:
		memberStats = memberStats.
			Select(`invitees.inviter_id AS inviter_id,
				COUNT(*) AS invited_count,
				SUM(CASE WHEN invitees.referral_qualified_at > 0 THEN 1 ELSE 0 END) AS qualified_count,
				COALESCE(SUM(invitees.referral_topup_quota), 0) AS topup_quota`).
			Group("invitees.inviter_id")
		commissionStats = commissionStats.
			Select(`ranked_commissions.inviter_id AS inviter_id,
				COALESCE(SUM(ranked_commissions.reward_quota), 0) AS commission_quota,
				COUNT(*) AS commission_count`).
			Group("ranked_commissions.inviter_id")
	case AdminReferralRankingPeriodToday:
		// Keep every metric on the same acquisition cohort: users registered
		// through an invitation during the requested local calendar day. Using
		// qualification or commission timestamps here would mix older invitees
		// into today's ranking and make legacy migration timestamps look like
		// new customer acquisition.
		memberStats = memberStats.
			Select(`invitees.inviter_id AS inviter_id,
				COUNT(*) AS invited_count,
				SUM(CASE WHEN invitees.referral_qualified_at > 0 THEN 1 ELSE 0 END) AS qualified_count,
				COALESCE(SUM(invitees.referral_topup_quota), 0) AS topup_quota`).
			Where("invitees.created_at >= ? AND invitees.created_at < ?", startTime, endTime).
			Group("invitees.inviter_id")
		commissionStats = commissionStats.
			Joins(`JOIN users AS ranked_invitees
				ON ranked_invitees.id = ranked_commissions.invitee_id
				AND ranked_invitees.inviter_id = ranked_commissions.inviter_id`).
			Select(`ranked_commissions.inviter_id AS inviter_id,
				COALESCE(SUM(ranked_commissions.reward_quota), 0) AS commission_quota,
				COUNT(*) AS commission_count`).
			Where(`ranked_invitees.referral_eligible = ?
				AND ranked_invitees.deleted_at IS NULL
				AND ranked_invitees.created_at >= ?
				AND ranked_invitees.created_at < ?`, true, startTime, endTime).
			Group("ranked_commissions.inviter_id")
	default:
		return nil, 0, errors.New("invalid referral ranking period")
	}

	query := DB.Table("users AS inviters").
		Joins("LEFT JOIN (?) AS member_stats ON member_stats.inviter_id = inviters.id", memberStats).
		Joins("LEFT JOIN (?) AS commission_stats ON commission_stats.inviter_id = inviters.id", commissionStats).
		Where("inviters.deleted_at IS NULL")
	query = query.Where(`COALESCE(member_stats.invited_count, 0) > 0 OR
		COALESCE(member_stats.qualified_count, 0) > 0 OR
		COALESCE(` + selectedTopUpQuota + `, 0) > 0 OR
		COALESCE(commission_stats.commission_quota, 0) > 0 OR
		COALESCE(commission_stats.commission_count, 0) > 0`)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rankings []*AdminReferralRanking
	selectColumns := `inviters.id AS user_id,
		inviters.username AS username,
		inviters.display_name AS display_name,
		COALESCE(member_stats.invited_count, 0) AS invited_count,
		COALESCE(member_stats.qualified_count, 0) AS qualified_count,
		COALESCE(` + selectedTopUpQuota + `, 0) AS topup_quota,
		COALESCE(commission_stats.commission_quota, 0) AS commission_quota,
		COALESCE(commission_stats.commission_count, 0) AS commission_count`
	if err := query.Select(selectColumns).
		Order("qualified_count DESC").
		Order("topup_quota DESC").
		Order("invited_count DESC").
		Order("user_id ASC").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).
		Scan(&rankings).Error; err != nil {
		return nil, 0, err
	}
	return rankings, total, nil
}

func resolveAdminReferralRankingRange(filter AdminReferralRankingFilter, now time.Time) (int64, int64, error) {
	switch filter.Period {
	case AdminReferralRankingPeriodAll:
		if filter.StartTime != 0 || filter.EndTime != 0 {
			return 0, 0, errors.New("time range is only valid for today's referral rankings")
		}
		return 0, 0, nil
	case AdminReferralRankingPeriodToday:
		if (filter.StartTime == 0) != (filter.EndTime == 0) {
			return 0, 0, errors.New("start_time and end_time must be provided together")
		}
		if filter.StartTime != 0 {
			nowUnix := now.Unix()
			if filter.StartTime <= 0 || filter.EndTime <= filter.StartTime || filter.EndTime-filter.StartTime > 26*60*60 || filter.StartTime > nowUnix || filter.EndTime <= nowUnix {
				return 0, 0, errors.New("invalid referral ranking time range")
			}
			return filter.StartTime, filter.EndTime, nil
		}
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix(), nil
	default:
		return 0, 0, errors.New("invalid referral ranking period")
	}
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
	for _, commission := range commissions {
		commission.InviteeName = maskReferralMemberUsername(commission.InviteeName)
	}
	return commissions, total, nil
}
