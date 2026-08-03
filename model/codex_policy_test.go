package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func createCodexPolicyTestUser(t *testing.T, role int) User {
	t.Helper()
	suffix := time.Now().UnixNano()
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
			Action: "block", Profile: "strict", ReasonCode: "policy_test",
			Severity: "high", StrikeEligible: true, RuleVersion: "0123456789abcdef",
			EvidenceSHA256:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SignatureVersion: "v1", CreatedAt: createdAt,
		},
		AuditEnabled: true, StrikeEnabled: true, BanAfter: 2, WindowSeconds: 3600,
	}
}
