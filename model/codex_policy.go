package model

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CodexPolicyReasonUpstreamCyberPolicy = "upstream_cyber_policy"

var ErrPolicyIPBlocked = errors.New("access denied by IP policy; contact an administrator")

// Administrators remain able to recover a shared-IP deployment. Identity and
// role must be authenticated before calling this; client headers are not roles.
func CheckCodexPolicyAccess(role int, ip string) error {
	if role >= common.RoleAdminUser {
		return nil
	}
	config, err := common.ReadCodexPolicyIPEnforcement()
	if err != nil || !config.Enabled {
		return err
	}
	blocked, err := IsCodexPolicyIPBlocked(ip, time.Now())
	if err != nil {
		return err
	}
	if blocked {
		return ErrPolicyIPBlocked
	}
	return nil
}

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
	IP           string `json:"ip" gorm:"size:64;primaryKey"`
	UserID       int    `json:"user_id" gorm:"not null;index"`
	DecisionID   string `json:"decision_id" gorm:"size:128;not null"`
	CreatedAt    int64  `json:"created_at" gorm:"not null"`
	ExpiresAt    int64  `json:"expires_at" gorm:"not null;index"`
	RevokedAt    int64  `json:"revoked_at" gorm:"not null;default:0;index"`
	RevokedBy    int    `json:"revoked_by" gorm:"not null;default:0"`
	RevokeReason string `json:"revoke_reason" gorm:"size:300;not null;default:''"`
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
		// Account punishment is intentionally narrower than the signed audit
		// protocol: only an explicit upstream CYB decision may add a strike. Local
		// prompt rules, external-review verdicts and ordinary upstream 4xx errors
		// remain auditable but can never disable an account.
		decision.StrikeCounted = input.StrikeEnabled && decision.StrikeEligible && decision.ReasonCode == CodexPolicyReasonUpstreamCyberPolicy && user.Role < common.RoleAdminUser

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
		if !decision.StrikeCounted {
			return nil
		}
		state := CodexPolicyUserState{UserID: user.Id}
		if err := tx.Where("user_id = ?", user.Id).FirstOrCreate(&state).Error; err != nil {
			return err
		}
		state.Revision++
		if err := tx.Save(&state).Error; err != nil {
			return err
		}

		windowStart := decision.CreatedAt - int64(input.WindowSeconds)
		if err := tx.Model(&CodexPolicyDecision{}).
			Where("user_id = ? AND strike_counted = ? AND created_at >= ? AND id > ?", decision.UserID, true, windowStart, state.ResetAfterID).
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
			state.BannedAuthVersion = bannedAuthVersion
			if err := tx.Save(&state).Error; err != nil {
				return err
			}
			if err := tx.Create(&CodexPolicyRestrictionEvent{UserID: user.Id, DecisionID: decision.DecisionID, Action: "disable_account", CreatedAt: decision.CreatedAt}).Error; err != nil {
				return err
			}
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
				DoUpdates: clause.AssignmentColumns([]string{"user_id", "decision_id", "created_at", "expires_at", "revoked_at", "revoked_by", "revoke_reason"}),
			}).Create(&block).Error; err != nil {
				return err
			}
			result.IPBlocked = true
			if err := tx.Create(&CodexPolicyRestrictionEvent{IP: block.IP, UserID: user.Id, DecisionID: decision.DecisionID, Action: "block_ip", CreatedAt: decision.CreatedAt}).Error; err != nil {
				return err
			}
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
	err := DB.Model(&CodexPolicyIPBlock{}).Where("ip = ? AND expires_at > ? AND revoked_at = ?", ip, now.Unix(), 0).Count(&count).Error
	return count > 0, err
}

func isCodexPolicyDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate key") || strings.Contains(message, "23505")
}
