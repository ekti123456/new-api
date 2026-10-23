package model

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplyCodexPolicyDecisionCountsStrikesAndRejectsReplay(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	createdAt := time.Now().Unix()
	input := codexPolicyTestInput(user.Id, "dec_first", createdAt)

	first, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.StrikeCount)
	assert.False(t, first.AccountBanned)

	replay, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.True(t, replay.Duplicate)

	input.Decision.DecisionID = "dec_second"
	input.Decision.CreatedAt++
	second, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(2), second.StrikeCount)

	var stored int64
	require.NoError(t, DB.Model(&CodexPolicyDecision{}).Where("user_id = ?", user.Id).Count(&stored).Error)
	assert.Equal(t, int64(2), stored)
}

func TestApplyCodexPolicyDecisionProtectsAdministrators(t *testing.T) {
	truncateTables(t)
	admin := createCodexPolicyTestUser(t, common.RoleAdminUser)
	input := codexPolicyTestInput(admin.Id, "dec_admin", time.Now().Unix())
	input.AccountBanEnabled = true
	input.IPBlockEnabled = true
	input.BanAfter = 1

	result, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.True(t, result.Protected)
	assert.False(t, result.AccountBanned)
	assert.False(t, result.IPBlocked)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, admin.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, reloaded.Status)
}

func TestApplyCodexPolicyDecisionDoesNotCountAuditOnlyHistory(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	createdAt := time.Now().Unix()
	auditOnly := codexPolicyTestInput(user.Id, "dec_audit_only", createdAt)
	auditOnly.StrikeEnabled = false
	auditOnly.AccountBanEnabled = false

	result, err := ApplyCodexPolicyDecision(auditOnly)
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.StrikeCount)

	firstEnforced := codexPolicyTestInput(user.Id, "dec_first_enforced", createdAt+1)
	firstEnforced.AccountBanEnabled = true
	result, err = ApplyCodexPolicyDecision(firstEnforced)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.StrikeCount)
	assert.False(t, result.AccountBanned)
}

func TestApplyCodexPolicyDecisionDoesNotCountNonCYBDecision(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	input := codexPolicyTestInput(user.Id, "dec_local_prompt", time.Now().Unix())
	input.Decision.ReasonCode = "prompt_policy_match"
	input.AccountBanEnabled = true
	input.BanAfter = 1

	result, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.StrikeCount)
	assert.False(t, result.AccountBanned)

	var stored CodexPolicyDecision
	require.NoError(t, DB.Where("decision_id = ?", input.Decision.DecisionID).First(&stored).Error)
	assert.False(t, stored.StrikeCounted)
}

func TestApplyCodexPolicyDecisionRebansAfterManualUnbanOnEveryNewCYB(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	createdAt := time.Now().Unix()
	input := codexPolicyTestInput(user.Id, "dec_cyb_first", createdAt)
	input.AccountBanEnabled = true
	input.IPBlockEnabled = true
	input.WindowSeconds = 7 * 24 * 60 * 60

	first, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.StrikeCount)
	assert.False(t, first.AccountBanned)

	input.Decision.DecisionID = "dec_cyb_second"
	input.Decision.CreatedAt++
	second, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(2), second.StrikeCount)
	assert.True(t, second.AccountBanned)

	for index, decisionID := range []string{"dec_cyb_third", "dec_cyb_fourth"} {
		snapshot, err := GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
		require.NoError(t, err)
		require.True(t, snapshot.PolicyDisabled)
		require.Len(t, snapshot.ActiveIPs, 1)
		release := CodexPolicyRelease{
			UserID: user.Id, StateRevision: &snapshot.State.Revision, UserAuthVersion: snapshot.UserAuthVersion,
			IPs:            []CodexPolicyIPRelease{{IP: snapshot.ActiveIPs[0].IP, DecisionID: snapshot.ActiveIPs[0].DecisionID}},
			RestoreAccount: true, Reason: "reviewed without resetting strikes",
		}
		require.NoError(t, ReleaseCodexPolicyRestrictions(release, 99))
		unblocked, err := GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
		require.NoError(t, err)
		assert.Equal(t, common.UserStatusEnabled, unblocked.UserStatus)
		assert.Equal(t, snapshot.ActiveStrikes, unblocked.ActiveStrikes)
		assert.Zero(t, unblocked.ActiveIPCount)

		input.Decision.DecisionID = decisionID
		input.Decision.CreatedAt++
		next, err := ApplyCodexPolicyDecision(input)
		require.NoError(t, err)
		assert.Equal(t, int64(3+index), next.StrikeCount)
		assert.True(t, next.AccountBanned)
		assert.True(t, next.IPBlocked)
		blocked, err := IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Now())
		require.NoError(t, err)
		assert.True(t, blocked, "a manual release must not exempt new violations")
		require.ErrorIs(t, ReleaseCodexPolicyRestrictions(release, 99), ErrPolicyRestrictionsChanged)
	}
}

func TestApplyCodexPolicyDecisionExpiresCYBOutsideWindow(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	createdAt := time.Now().Unix()
	input := codexPolicyTestInput(user.Id, "dec_cyb_expired", createdAt-7*24*60*60-1)
	input.AccountBanEnabled = true
	input.WindowSeconds = 7 * 24 * 60 * 60

	_, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	input.Decision.DecisionID = "dec_cyb_current"
	input.Decision.CreatedAt = createdAt
	result, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.StrikeCount)
	assert.False(t, result.AccountBanned)
}

func TestApplyCodexPolicyDecisionCanDisableAccountAndTemporarilyBlockIP(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	createdAt := time.Now().Unix()
	input := codexPolicyTestInput(user.Id, "dec_ban_first", createdAt)
	input.AccountBanEnabled = true
	input.IPBlockEnabled = true

	_, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	input.Decision.DecisionID = "dec_ban_second"
	input.Decision.CreatedAt++
	result, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.True(t, result.AccountBanned)
	assert.True(t, result.IPBlocked)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, reloaded.Status)
	assert.Greater(t, reloaded.AuthVersion, int64(1))

	blocked, err := IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Unix(input.Decision.CreatedAt+1, 0))
	require.NoError(t, err)
	assert.True(t, blocked)
	blocked, err = IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Unix(input.Decision.CreatedAt+int64(input.WindowSeconds)+1, 0))
	require.NoError(t, err)
	assert.False(t, blocked)
}

func TestPolicyReleasePreservesHistoryAndRejectsStaleConfirmation(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	input := codexPolicyTestInput(user.Id, "release-first", time.Now().Unix())
	input.AccountBanEnabled, input.IPBlockEnabled = true, true
	_, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	input.Decision.DecisionID = "release-second"
	_, err = ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	snapshot, err := GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	require.True(t, snapshot.PolicyDisabled)
	require.Len(t, snapshot.ActiveIPs, 1)
	request := CodexPolicyRelease{UserID: user.Id, StateRevision: &snapshot.State.Revision, UserAuthVersion: snapshot.UserAuthVersion,
		IPs:            []CodexPolicyIPRelease{{IP: snapshot.ActiveIPs[0].IP, DecisionID: snapshot.ActiveIPs[0].DecisionID}},
		RestoreAccount: true, ResetStrikes: true, Reason: "reviewed"}
	input.Decision.DecisionID = "release-third"
	_, err = ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	require.ErrorIs(t, ReleaseCodexPolicyRestrictions(request, 99), ErrPolicyRestrictionsChanged)
	snapshot, err = GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	assert.True(t, snapshot.PolicyDisabled)
	assert.Equal(t, int64(3), snapshot.ActiveStrikes)
	request.StateRevision = &snapshot.State.Revision
	request.IPs[0].DecisionID = snapshot.ActiveIPs[0].DecisionID
	require.NoError(t, ReleaseCodexPolicyRestrictions(request, 99))
	require.ErrorIs(t, ReleaseCodexPolicyRestrictions(request, 99), ErrPolicyRestrictionsChanged)
	snapshot, err = GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusEnabled, snapshot.UserStatus)
	assert.Zero(t, snapshot.ActiveStrikes)
	assert.Zero(t, snapshot.ActiveIPCount)
	var historyCount int64
	require.NoError(t, DB.Model(&CodexPolicyDecision{}).Where("user_id = ?", user.Id).Count(&historyCount).Error)
	assert.Equal(t, int64(3), historyCount)
	replay, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.True(t, replay.Duplicate)
	input.Decision.DecisionID = "after-reset-first"
	next, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.Equal(t, int64(1), next.StrikeCount)
	assert.False(t, next.AccountBanned)
	assert.False(t, next.IPBlocked)
	input.Decision.DecisionID = "after-reset-second"
	next, err = ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	assert.True(t, next.AccountBanned)
	assert.True(t, next.IPBlocked)
	blocked, err := IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Now())
	require.NoError(t, err)
	assert.True(t, blocked, "new violations reaching the threshold must replace the revoked IP ban")
	snapshot, err = GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	assert.Equal(t, int64(2), snapshot.ActiveStrikes)
	assert.True(t, snapshot.PolicyDisabled)
	require.Len(t, snapshot.ActiveIPs, 1)
	assert.Zero(t, snapshot.ActiveIPs[0].RevokedBy)
	assert.Empty(t, snapshot.ActiveIPs[0].RevokeReason)
}

func TestPolicyIPOnlyReleaseAndManualAccountDisableRemainIndependent(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	input := codexPolicyTestInput(user.Id, "separate-bans", time.Now().Unix())
	input.AccountBanEnabled, input.IPBlockEnabled, input.BanAfter = true, true, 1
	_, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	require.NoError(t, user.DisableByAdministrator())
	detail, err := GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	assert.False(t, detail.PolicyDisabled)
	request := CodexPolicyRelease{UserID: user.Id, StateRevision: &detail.State.Revision, UserAuthVersion: detail.UserAuthVersion,
		IPs: []CodexPolicyIPRelease{{IP: "::ffff:" + input.Decision.ClientIP, DecisionID: input.Decision.DecisionID}}, RestoreAccount: true, Reason: "reviewed"}
	require.ErrorIs(t, ReleaseCodexPolicyRestrictions(request, 99), ErrAccountNotPolicyDisabled)
	request.RestoreAccount = false
	require.NoError(t, ReleaseCodexPolicyRestrictions(request, 99))
	detail, err = GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusDisabled, detail.UserStatus)
	assert.Equal(t, int64(1), detail.ActiveStrikes)
	rows, total, err := ListCodexPolicyIPBlocks(input.Decision.ClientIP, user.Id, "revoked", 0, 50, time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, 99, rows[0].RevokedBy)
	assert.Equal(t, input.Decision.RequestID, rows[0].RequestID)
}

func TestPolicySharedIPNewDecisionAndDeletedUserRelease(t *testing.T) {
	truncateTables(t)
	first := createCodexPolicyTestUser(t, common.RoleCommonUser)
	second := createCodexPolicyTestUser(t, common.RoleCommonUser)
	input := codexPolicyTestInput(first.Id, "shared-first", time.Now().Unix())
	input.IPBlockEnabled, input.BanAfter = true, 1
	_, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	old := CodexPolicyRelease{UserID: first.Id, IPs: []CodexPolicyIPRelease{{IP: input.Decision.ClientIP, DecisionID: input.Decision.DecisionID}}, Reason: "reviewed"}
	input.Decision.UserID, input.Decision.DecisionID = second.Id, "shared-second"
	_, err = ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	require.ErrorIs(t, ReleaseCodexPolicyRestrictions(old, 99), ErrPolicyRestrictionsChanged)
	blocked, err := IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Now())
	require.NoError(t, err)
	assert.True(t, blocked)
	require.NoError(t, DB.Unscoped().Delete(&second).Error)
	current := CodexPolicyRelease{UserID: second.Id, IPs: []CodexPolicyIPRelease{{IP: input.Decision.ClientIP, DecisionID: input.Decision.DecisionID}}, Reason: "deleted user IP review"}
	require.NoError(t, ReleaseCodexPolicyRestrictions(current, 99))
	blocked, err = IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Now())
	require.NoError(t, err)
	assert.False(t, blocked)
}

func TestPolicyReleaseRacingNewDecisionCannotRemoveNewBan(t *testing.T) {
	truncateTables(t)
	user := createCodexPolicyTestUser(t, common.RoleCommonUser)
	input := codexPolicyTestInput(user.Id, "concurrent-initial", time.Now().Unix())
	input.AccountBanEnabled, input.IPBlockEnabled, input.BanAfter = true, true, 1
	_, err := ApplyCodexPolicyDecision(input)
	require.NoError(t, err)
	snapshot, err := GetCodexPolicyRestrictions(user.Id, input.WindowSeconds, time.Now().Unix())
	require.NoError(t, err)
	release := CodexPolicyRelease{UserID: user.Id, StateRevision: &snapshot.State.Revision, UserAuthVersion: snapshot.UserAuthVersion,
		IPs: []CodexPolicyIPRelease{{IP: input.Decision.ClientIP, DecisionID: input.Decision.DecisionID}}, RestoreAccount: true, Reason: "reviewed"}
	input.Decision.DecisionID = "concurrent-new"
	start := make(chan struct{})
	decisionDone, releaseDone := make(chan error, 1), make(chan error, 1)
	go func() { <-start; _, err := ApplyCodexPolicyDecision(input); decisionDone <- err }()
	go func() { <-start; releaseDone <- ReleaseCodexPolicyRestrictions(release, 99) }()
	close(start)
	require.NoError(t, <-decisionDone)
	if err := <-releaseDone; err != nil {
		require.ErrorIs(t, err, ErrPolicyRestrictionsChanged)
	}
	blocked, err := IsCodexPolicyIPBlocked(input.Decision.ClientIP, time.Now())
	require.NoError(t, err)
	assert.True(t, blocked, "the new ban must survive either ordering")
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, user.Status)
}

func TestPolicyLegacyIPMigrationAndReleaseSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy-upgrade.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	legacy := struct {
		IP         string `gorm:"size:64;primaryKey"`
		UserID     int    `gorm:"not null;index"`
		DecisionID string `gorm:"size:128;not null"`
		CreatedAt  int64  `gorm:"not null"`
		ExpiresAt  int64  `gorm:"not null;index"`
	}{IP: "2001:db8::1", UserID: 15, DecisionID: "legacy", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Table("codex_policy_ip_blocks").AutoMigrate(&legacy))
	require.NoError(t, db.Table("codex_policy_ip_blocks").Create(&legacy).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&User{}, &CodexPolicyIPBlock{}, &CodexPolicyUserState{}, &CodexPolicyRestrictionEvent{}))
	}
	oldDB := DB
	DB = db
	t.Cleanup(func() { DB = oldDB })
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	blocked, err := IsCodexPolicyIPBlocked("2001:0db8:0:0:0:0:0:1", time.Now())
	require.NoError(t, err)
	assert.True(t, blocked, "new revoked fields must default to zero for old rows")
	require.NoError(t, ReleaseCodexPolicyRestrictions(CodexPolicyRelease{UserID: 15, IPs: []CodexPolicyIPRelease{{IP: legacy.IP, DecisionID: legacy.DecisionID}}, Reason: "reviewed after upgrade"}, 99))
	require.NoError(t, sqlDB.Close())
	DB, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	reopened, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	blocked, err = IsCodexPolicyIPBlocked(legacy.IP, time.Now())
	require.NoError(t, err)
	assert.False(t, blocked, "revocation must persist without local caches")
}

var codexPolicyTestSequence atomic.Int64

func createCodexPolicyTestUser(t *testing.T, role int) User {
	t.Helper()
	suffix := codexPolicyTestSequence.Add(1)
	user := User{
		Username: fmt.Sprintf("policy-%d", suffix), Password: "password-placeholder",
		Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		AffCode: fmt.Sprintf("policy-aff-%d", suffix),
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func codexPolicyTestInput(userID int, decisionID string, createdAt int64) CodexPolicyDecisionInput {
	return CodexPolicyDecisionInput{
		Decision: CodexPolicyDecision{
			DecisionID: decisionID, RequestID: "req_policy", UserID: userID,
			ClientIP: "203.0.113.42", PlatformID: "newapi", ChannelID: 7,
			Action: "block", Profile: "strict", ReasonCode: CodexPolicyReasonUpstreamCyberPolicy,
			Severity: "high", StrikeEligible: true, RuleVersion: "0123456789abcdef",
			EvidenceSHA256:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SignatureVersion: "v1", CreatedAt: createdAt,
		},
		AuditEnabled: true, StrikeEnabled: true, BanAfter: 2, WindowSeconds: 3600,
	}
}
