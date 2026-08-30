package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const legacyReferralMigrationKey = "migration.referral_legacy_v1"

var legacyReferralMigrationMu sync.Mutex

type legacyReferralUser struct {
	Id                  int
	InviterId           int
	ReferralTopUpQuota  int64
	ReferralQualifiedAt int64
}

type legacyReferralMigrationStats struct {
	Members             int
	RecognizedTopUps    int
	SkippedTopUps       int
	QualifiedMembers    int
	MigrationWasApplied bool
}

// InitializeLegacyReferrals activates traceable invitation relationships that
// predate the referral commission program. Historical successful online
// top-ups establish cumulative qualification only; this migration never
// creates commissions or changes any reward balance.
func InitializeLegacyReferrals() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	legacyReferralMigrationMu.Lock()
	defer legacyReferralMigrationMu.Unlock()
	stats := legacyReferralMigrationStats{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		marker := Option{Key: legacyReferralMigrationKey, Value: "running"}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where(commonKeyCol+" = ?", legacyReferralMigrationKey).First(&marker).Error; err != nil {
				return err
			}
			if marker.Value == "completed" {
				return nil
			}
			return errors.New("legacy referral migration is already running")
		}
		stats.MigrationWasApplied = true

		migrationTime := common.GetTimestamp()
		qualifiedThreshold := int64(common.QuotaFromFloat(float64(common.ReferralQualifiedTopUpUnits) * common.QuotaPerUnit))
		if qualifiedThreshold <= 0 {
			return errors.New("invalid referral qualification threshold")
		}

		const batchSize = 500
		lastUserId := 0
		qualifiedByInviter := make(map[int]int)
		for {
			var users []legacyReferralUser
			if err := tx.Table("users AS invitees").
				Select("invitees.id, invitees.inviter_id, invitees.referral_topup_quota, invitees.referral_qualified_at").
				Joins("JOIN users AS inviters ON inviters.id = invitees.inviter_id AND inviters.deleted_at IS NULL").
				Where("invitees.id > ? AND invitees.inviter_id > 0 AND invitees.inviter_id <> invitees.id AND (invitees.referral_eligible = ? OR invitees.referral_eligible IS NULL) AND invitees.deleted_at IS NULL", lastUserId, false).
				Order("invitees.id ASC").Limit(batchSize).Scan(&users).Error; err != nil {
				return err
			}
			if len(users) == 0 {
				break
			}
			stats.Members += len(users)

			userIds := make([]int, len(users))
			for index := range users {
				userIds[index] = users[index].Id
			}
			if err := tx.Model(&User{}).Where("id IN ?", userIds).Updates(map[string]interface{}{
				"referral_eligible": true,
				"referral_legacy":   true,
			}).Error; err != nil {
				return err
			}

			totals := make(map[int]int64, len(users))
			lastTopUpId := 0
			for {
				var topUps []TopUp
				if err := tx.Select("id, user_id, amount, money, trade_no, payment_method, payment_provider, credited_quota").
					Where("id > ? AND user_id IN ? AND status = ? AND (amount > 0 OR credited_quota > 0)", lastTopUpId, userIds, common.TopUpStatusSuccess).
					Where("NOT EXISTS (SELECT 1 FROM subscription_orders WHERE subscription_orders.trade_no = top_ups.trade_no)").
					Order("id ASC").Limit(batchSize).Find(&topUps).Error; err != nil {
					return err
				}
				if len(topUps) == 0 {
					break
				}
				for index := range topUps {
					creditedQuota := historicalTopUpCreditedQuota(&topUps[index])
					if creditedQuota <= 0 {
						stats.SkippedTopUps++
						continue
					}
					stats.RecognizedTopUps++
					current := totals[topUps[index].UserId]
					if current > math.MaxInt64-int64(creditedQuota) {
						return fmt.Errorf("historical referral top-up quota overflow for user %d", topUps[index].UserId)
					}
					totals[topUps[index].UserId] = current + int64(creditedQuota)
				}
				lastTopUpId = topUps[len(topUps)-1].Id
			}

			for index := range users {
				total := totals[users[index].Id]
				if users[index].ReferralTopUpQuota > total {
					total = users[index].ReferralTopUpQuota
				}
				qualifiedAt := users[index].ReferralQualifiedAt
				if qualifiedAt == 0 && total >= qualifiedThreshold {
					qualifiedAt = migrationTime
				}
				if err := tx.Model(&User{}).Where("id = ?", users[index].Id).Updates(map[string]interface{}{
					"referral_topup_quota":  total,
					"referral_qualified_at": qualifiedAt,
				}).Error; err != nil {
					return err
				}
				if qualifiedAt > 0 {
					qualifiedByInviter[users[index].InviterId]++
					stats.QualifiedMembers++
				}
			}
			lastUserId = users[len(users)-1].Id
		}

		for inviterId, count := range qualifiedByInviter {
			if err := tx.Model(&User{}).Where("id = ?", inviterId).
				Update("aff_qualified_count", gorm.Expr("aff_qualified_count + ?", count)).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Option{}).Where(commonKeyCol+" = ?", legacyReferralMigrationKey).Update("value", "completed").Error
	})
	if err != nil {
		return err
	}
	if stats.MigrationWasApplied {
		common.SysLog(fmt.Sprintf(
			"legacy referral migration completed: members=%d recognized_topups=%d skipped_ambiguous_or_invalid_topups=%d qualified_members=%d",
			stats.Members,
			stats.RecognizedTopUps,
			stats.SkippedTopUps,
			stats.QualifiedMembers,
		))
	}
	return nil
}

func historicalTopUpCreditedQuota(topUp *TopUp) int {
	if topUp == nil {
		return 0
	}
	provider := strings.ToLower(strings.TrimSpace(topUp.PaymentProvider))
	method := strings.ToLower(strings.TrimSpace(topUp.PaymentMethod))
	if provider == PaymentProviderBalance || method == PaymentMethodBalance {
		return 0
	}
	switch provider {
	case "admin", "manual", "system", "redemption", "subscription":
		return 0
	}
	switch method {
	case "admin", "manual", "system", "redemption", "subscription":
		return 0
	}
	if topUp.CreditedQuota > 0 {
		return topUp.CreditedQuota
	}
	if topUp.Amount <= 0 {
		return 0
	}
	switch provider {
	case PaymentProviderStripe:
		creditedQuota, clamp := common.QuotaFromFloatChecked(topUp.Money * common.QuotaPerUnit)
		if clamp != nil {
			return 0
		}
		return creditedQuota
	case PaymentProviderCreem:
		creditedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(topUp.Amount))
		if clamp != nil {
			return 0
		}
		return creditedQuota
	case PaymentProviderEpay, PaymentProviderWaffo, PaymentProviderWaffoPancake:
		creditedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Truncate(0))
		if clamp != nil {
			return 0
		}
		return creditedQuota
	case "":
		switch method {
		case PaymentMethodStripe:
			creditedQuota, clamp := common.QuotaFromFloatChecked(topUp.Money * common.QuotaPerUnit)
			if clamp != nil {
				return 0
			}
			return creditedQuota
		case PaymentMethodCreem:
			creditedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(topUp.Amount))
			if clamp != nil {
				return 0
			}
			return creditedQuota
		case PaymentMethodBalance:
			return 0
		case PaymentMethodWaffo, PaymentMethodWaffoPancake:
			creditedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Truncate(0))
			if clamp != nil {
				return 0
			}
			return creditedQuota
		}
		if method != "" {
			// Epay payment methods are operator-defined (for example, alipay
			// or wxpay), so any other non-empty legacy method is an Epay row.
			creditedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Truncate(0))
			if clamp != nil {
				return 0
			}
			return creditedQuota
		}
		tradeNo := strings.ToUpper(topUp.TradeNo)
		if strings.HasPrefix(tradeNo, "USR") || strings.HasPrefix(tradeNo, "A") ||
			strings.HasPrefix(tradeNo, "WAFFO-") || strings.HasPrefix(tradeNo, "WAFFO_PANCAKE-") {
			creditedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Truncate(0))
			if clamp != nil {
				return 0
			}
			return creditedQuota
		}
		// Empty-method ref_ rows can be either Stripe or Creem and use
		// incompatible quota formulas. Skipping them is safer than
		// manufacturing an incorrect qualification total.
		return 0
	default:
		return 0
	}
}
