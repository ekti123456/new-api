package model

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// These rows are administrative state, never request-path caches. User row
// locks serialize policy decisions, manual status changes and policy releases.
type CodexPolicyUserState struct {
	UserID            int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Revision          int64 `json:"revision" gorm:"not null"`
	ResetAfterID      int64 `json:"reset_after_id" gorm:"not null"`
	BannedAuthVersion int64 `json:"-" gorm:"not null"`
}

type CodexPolicyRestrictionEvent struct {
	ID         int64  `json:"id" gorm:"primaryKey"`
	IP         string `json:"ip" gorm:"size:64;not null;index"`
	UserID     int    `json:"user_id" gorm:"not null;index"`
	DecisionID string `json:"decision_id" gorm:"size:128;not null"`
	Action     string `json:"action" gorm:"size:32;not null"`
	ActorID    int    `json:"actor_id" gorm:"not null"`
	Reason     string `json:"reason" gorm:"size:300;not null"`
	CreatedAt  int64  `json:"created_at" gorm:"not null;index"`
}

type CodexPolicyIPBlockView struct {
	CodexPolicyIPBlock
	RequestID  string `json:"request_id"`
	ReasonCode string `json:"reason_code"`
}

func ListCodexPolicyIPBlocks(ip string, userID int, status string, offset, limit int, now int64) ([]CodexPolicyIPBlockView, int64, error) {
	query := DB.Model(&CodexPolicyIPBlock{})
	if ip != "" {
		parsed := net.ParseIP(strings.TrimSpace(ip))
		if parsed == nil {
			return nil, 0, fmt.Errorf("invalid IP address")
		}
		query = query.Where("codex_policy_ip_blocks.ip = ?", parsed.String())
	}
	if userID > 0 {
		query = query.Where("codex_policy_ip_blocks.user_id = ?", userID)
	}
	switch status {
	case "active", "":
		query = query.Where("revoked_at = ? AND expires_at > ?", 0, now)
	case "expired":
		query = query.Where("revoked_at = ? AND expires_at <= ?", 0, now)
	case "revoked":
		query = query.Where("revoked_at > ?", 0)
	case "all":
	default:
		return nil, 0, fmt.Errorf("invalid restriction status")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows := []CodexPolicyIPBlockView{}
	err := query.Select("codex_policy_ip_blocks.*, codex_policy_decisions.request_id, codex_policy_decisions.reason_code").
		Joins("LEFT JOIN codex_policy_decisions ON codex_policy_decisions.decision_id = codex_policy_ip_blocks.decision_id").
		Order("codex_policy_ip_blocks.created_at DESC").Order("codex_policy_ip_blocks.ip ASC").Offset(offset).Limit(limit).Scan(&rows).Error
	return rows, total, err
}

type CodexPolicyRestrictionDetails struct {
	UserID          int                           `json:"user_id"`
	Username        string                        `json:"username"`
	UserStatus      int                           `json:"user_status"`
	UserAuthVersion int64                         `json:"user_auth_version"`
	PolicyDisabled  bool                          `json:"policy_disabled"`
	State           CodexPolicyUserState          `json:"state"`
	ActiveStrikes   int64                         `json:"active_strikes"`
	ActiveIPs       []CodexPolicyIPBlockView      `json:"active_ips"`
	ActiveIPCount   int64                         `json:"active_ip_count"`
	Events          []CodexPolicyRestrictionEvent `json:"events"`
}

func GetCodexPolicyRestrictions(userID int, windowSeconds int, now int64) (CodexPolicyRestrictionDetails, error) {
	detail := CodexPolicyRestrictionDetails{UserID: userID, State: CodexPolicyUserState{UserID: userID}, Events: []CodexPolicyRestrictionEvent{}}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		// A coherent confirmation snapshot; the write compares both versions.
		if err := lockForUpdate(tx).Unscoped().Where("id = ?", userID).First(&user).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		detail.Username, detail.UserStatus, detail.UserAuthVersion = user.Username, user.Status, user.AuthVersion
		if err := tx.Where("user_id = ?", userID).Find(&detail.State).Error; err != nil {
			return err
		}
		detail.PolicyDisabled = !user.DeletedAt.Valid && user.Status == common.UserStatusDisabled && detail.State.BannedAuthVersion == user.AuthVersion
		if err := tx.Model(&CodexPolicyDecision{}).Where("user_id = ? AND strike_counted = ? AND created_at >= ? AND id > ?", userID, true, now-int64(windowSeconds), detail.State.ResetAfterID).Count(&detail.ActiveStrikes).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Order("id DESC").Limit(20).Find(&detail.Events).Error
	})
	if err != nil {
		return detail, err
	}
	detail.ActiveIPs, detail.ActiveIPCount, err = ListCodexPolicyIPBlocks("", userID, "active", 0, 100, now)
	return detail, err
}

var ErrPolicyRestrictionsChanged = errors.New("restrictions changed; reload before releasing")
var ErrAccountNotPolicyDisabled = errors.New("account was not disabled by this policy; use user management")
var ErrInvalidPolicyRelease = errors.New("invalid restriction release")

type CodexPolicyIPRelease struct {
	IP         string `json:"ip"`
	DecisionID string `json:"decision_id"`
}

type CodexPolicyRelease struct {
	UserID          int                    `json:"user_id"`
	StateRevision   *int64                 `json:"state_revision"`
	UserAuthVersion int64                  `json:"user_auth_version"`
	IPs             []CodexPolicyIPRelease `json:"ips"`
	RestoreAccount  bool                   `json:"restore_account"`
	ResetStrikes    bool                   `json:"reset_strikes"`
	Reason          string                 `json:"reason"`
}

// Release is explicit, bounded and conditional. Replaying an old confirmation
// cannot delete a newer ban or clear strikes accumulated after that snapshot.
func ReleaseCodexPolicyRestrictions(input CodexPolicyRelease, actorID int) error {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.UserID <= 0 || actorID <= 0 || len(input.IPs) > 100 || len(input.Reason) > 300 || input.Reason == "" || (len(input.IPs) == 0 && !input.RestoreAccount && !input.ResetStrikes) {
		return ErrInvalidPolicyRelease
	}
	ips := append([]CodexPolicyIPRelease(nil), input.IPs...)
	seen := map[string]bool{}
	for i := range ips {
		ip := net.ParseIP(strings.TrimSpace(ips[i].IP))
		if ip == nil || ips[i].DecisionID == "" || len(ips[i].DecisionID) > 128 {
			return ErrInvalidPolicyRelease
		}
		ips[i].IP = ip.String()
		if seen[ips[i].IP] {
			return ErrInvalidPolicyRelease
		}
		seen[ips[i].IP] = true
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].IP < ips[j].IP })
	now := time.Now().Unix()
	var nextAuthVersion int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Unscoped().Where("id = ?", input.UserID).First(&user).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) || input.RestoreAccount || input.ResetStrikes {
				return err
			}
			// A deleted user must not make a surviving shared-IP ban permanent.
			user.Id = input.UserID
		}
		state := CodexPolicyUserState{UserID: user.Id}
		if err := tx.Where("user_id = ?", user.Id).Find(&state).Error; err != nil {
			return err
		}
		if input.ResetStrikes || input.RestoreAccount {
			if input.StateRevision == nil || *input.StateRevision != state.Revision || input.UserAuthVersion != user.AuthVersion {
				return ErrPolicyRestrictionsChanged
			}
		}
		if input.RestoreAccount {
			if user.DeletedAt.Valid || user.Role >= common.RoleAdminUser || user.Status != common.UserStatusDisabled || state.BannedAuthVersion != user.AuthVersion {
				return ErrAccountNotPolicyDisabled
			}
			var err error
			nextAuthVersion, err = IncrementUserAuthVersionWithTx(tx, user.Id)
			if err != nil {
				return err
			}
			if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error; err != nil {
				return err
			}
			state.BannedAuthVersion = 0
			if err := tx.Create(&CodexPolicyRestrictionEvent{UserID: user.Id, Action: "restore_account", ActorID: actorID, Reason: input.Reason, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		for _, item := range ips {
			result := tx.Model(&CodexPolicyIPBlock{}).Where("ip = ? AND user_id = ? AND decision_id = ? AND revoked_at = ? AND expires_at > ?", item.IP, user.Id, item.DecisionID, 0, now).
				Updates(map[string]interface{}{"revoked_at": now, "revoked_by": actorID, "revoke_reason": input.Reason})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrPolicyRestrictionsChanged
			}
			if err := tx.Create(&CodexPolicyRestrictionEvent{UserID: user.Id, IP: item.IP, DecisionID: item.DecisionID, Action: "release_ip", ActorID: actorID, Reason: input.Reason, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		if input.ResetStrikes {
			var last CodexPolicyDecision
			if err := tx.Where("user_id = ?", user.Id).Order("id DESC").Limit(1).Find(&last).Error; err != nil {
				return err
			}
			state.ResetAfterID = last.ID
			if err := tx.Create(&CodexPolicyRestrictionEvent{UserID: user.Id, Action: "reset_strikes", ActorID: actorID, Reason: input.Reason, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		state.Revision++
		return tx.Save(&state).Error
	})
	if err != nil {
		return err
	}
	if nextAuthVersion > 0 {
		if err := publishCommittedUserAuthVersion(input.UserID, nextAuthVersion); err != nil {
			return err
		}
		if err := PublishUserAuthCache(input.UserID); err != nil {
			return err
		}
		if err := InvalidateUserTokensCache(input.UserID); err != nil {
			return err
		}
	}
	return nil
}

// Used for an administrator's explicit disable, including an already disabled
// user. An automatic-policy release must never undo that separate decision.
func (user *User) DisableByAdministrator() error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", user.Id).First(user).Error; err != nil {
			return err
		}
		if user.Role == common.RoleRootUser {
			return errors.New("cannot disable the root user")
		}
		user.Status = common.UserStatusDisabled
		if err := user.UpdateWithTx(tx, false); err != nil {
			return err
		}
		return tx.Model(&CodexPolicyUserState{}).Where("user_id = ?", user.Id).Updates(map[string]interface{}{"banned_auth_version": 0, "revision": gorm.Expr("revision + ?", 1)}).Error
	}); err != nil {
		return err
	}
	if err := PublishUserAuthCache(user.Id); err != nil {
		return err
	}
	_, err := RevokeAllUserSessions(user.Id, "admin_disable")
	return err
}
