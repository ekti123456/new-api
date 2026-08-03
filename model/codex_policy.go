package model

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CodexPolicyDecision is the durable replay boundary and audit record for a
// verified Codex2API policy response. It stores only bounded metadata and an
// evidence digest; prompt text is deliberately not persisted here.
type CodexPolicyDecision struct {
	ID               int64  `gorm:"primaryKey"`
	DecisionID       string `gorm:"size:128;not null;uniqueIndex"`
	RequestID        string `gorm:"size:128;not null;index"`
	UserID           int    `gorm:"not null;index"`
	ClientIP         string `gorm:"size:64;not null;index"`
	PlatformID       string `gorm:"size:32;not null;index"`
	ChannelID        int    `gorm:"not null"`
	Action           string `gorm:"size:32;not null"`
	Profile          string `gorm:"size:32;not null"`
	ReasonCode       string `gorm:"size:128;not null"`
	Severity         string `gorm:"size:32;not null"`
	StrikeEligible   bool   `gorm:"not null;index"`
	StrikeCounted    bool   `gorm:"not null;index"`
	RuleVersion      string `gorm:"size:64;not null"`
	EvidenceSHA256   string `gorm:"size:64;not null"`
	SignatureVersion string `gorm:"size:16;not null"`
	CreatedAt        int64  `gorm:"not null;index"`
}

type CodexPolicyIPBlock struct {
	IP         string `gorm:"size:64;primaryKey"`
	UserID     int    `gorm:"not null;index"`
	DecisionID string `gorm:"size:128;not null"`
	CreatedAt  int64  `gorm:"not null"`
	ExpiresAt  int64  `gorm:"not null;index"`
}

type CodexPolicyDecisionInput struct {
	Decision          CodexPolicyDecision
	AuditEnabled      bool
	StrikeEnabled     bool
	AccountBanEnabled bool
	IPBlockEnabled    bool
	BanAfter          int
	WindowSeconds     int
}

type CodexPolicyDecisionResult struct {
	Duplicate     bool
	Protected     bool
	StrikeCount   int64
	AccountBanned bool
	IPBlocked     bool
}

func ApplyCodexPolicyDecision(input CodexPolicyDecisionInput) (CodexPolicyDecisionResult, error) {
	decision := input.Decision
	if decision.DecisionID == "" || decision.RequestID == "" || decision.UserID <= 0 {
		return CodexPolicyDecisionResult{}, fmt.Errorf("invalid Codex2API policy decision identity")
	}
	if input.BanAfter <= 0 || input.WindowSeconds <= 0 {
		return CodexPolicyDecisionResult{}, fmt.Errorf("invalid Codex2API policy strike window")
	}
	if decision.CreatedAt <= 0 {
		decision.CreatedAt = time.Now().Unix()
	}

	result := CodexPolicyDecisionResult{}
	var bannedAuthVersion int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "role", "status", "auth_version").Where("id = ?", decision.UserID).First(&user).Error; err != nil {
			return err
		}

		var existing int64
		if err := tx.Model(&CodexPolicyDecision{}).Where("decision_id = ?", decision.DecisionID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			result.Duplicate = true
			return nil
		}
		decision.StrikeCounted = input.StrikeEnabled && decision.StrikeEligible && user.Role < common.RoleAdminUser

		// Strike counting requires durable rows even when the optional audit
		// display is disabled, otherwise restarts could reset enforcement.
		if input.AuditEnabled || input.StrikeEnabled {
			if err := tx.Create(&decision).Error; err != nil {
				if isCodexPolicyDuplicateError(err) {
					result.Duplicate = true
					return nil
				}
				return err
			}
		}

		if user.Role >= common.RoleAdminUser {
			result.Protected = true
			return nil
		}
		if !input.StrikeEnabled || !decision.StrikeEligible {
			return nil
		}

		windowStart := decision.CreatedAt - int64(input.WindowSeconds)
		if err := tx.Model(&CodexPolicyDecision{}).
			Where("user_id = ? AND strike_counted = ? AND created_at >= ?", decision.UserID, true, windowStart).
			Count(&result.StrikeCount).Error; err != nil {
			return err
		}
		if result.StrikeCount < int64(input.BanAfter) {
			return nil
		}

		if input.AccountBanEnabled && user.Status == common.UserStatusEnabled {
			var err error
			bannedAuthVersion, err = IncrementUserAuthVersionWithTx(tx, user.Id)
			if err != nil {
				return err
			}
			if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error; err != nil {
				return err
			}
			result.AccountBanned = true
		}

		if input.IPBlockEnabled {
			parsedIP := net.ParseIP(decision.ClientIP)
			if parsedIP == nil {
				return nil
			}
			block := CodexPolicyIPBlock{
				IP: parsedIP.String(), UserID: decision.UserID, DecisionID: decision.DecisionID,
				CreatedAt: decision.CreatedAt, ExpiresAt: decision.CreatedAt + int64(input.WindowSeconds),
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "ip"}},
				DoUpdates: clause.AssignmentColumns([]string{"user_id", "decision_id", "created_at", "expires_at"}),
			}).Create(&block).Error; err != nil {
				return err
			}
			result.IPBlocked = true
		}
		return nil
	})
	if err != nil || result.Duplicate {
		return result, err
	}

	if result.AccountBanned {
		if err := publishCommittedUserAuthVersion(decision.UserID, bannedAuthVersion); err != nil {
			return result, err
		}
		if err := PublishUserAuthCache(decision.UserID); err != nil {
			return result, err
		}
		if _, err := RevokeAllUserSessions(decision.UserID, "codex2api_policy_violation"); err != nil {
			return result, err
		}
		if err := InvalidateUserTokensCache(decision.UserID); err != nil {
			return result, err
		}
		RecordLogWithAdminInfo(decision.UserID, LogTypeSystem, "Account disabled after verified Codex2API policy violations", map[string]interface{}{
			"decision_id":  decision.DecisionID,
			"request_id":   decision.RequestID,
			"reason_code":  decision.ReasonCode,
			"strike_count": result.StrikeCount,
		})
	}
	return result, nil
}

func IsCodexPolicyIPBlocked(ip string, now time.Time) (bool, error) {
	ip = strings.TrimSpace(ip)
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false, nil
	}
	ip = parsedIP.String()
	var count int64
	err := DB.Model(&CodexPolicyIPBlock{}).Where("ip = ? AND expires_at > ?", ip, now.Unix()).Count(&count).Error
	return count > 0, err
}

func isCodexPolicyDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate key") || strings.Contains(message, "23505")
}
