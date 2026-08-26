package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMarkCodexRootChannelAffinityUsedRecordsRedactedAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	const rootID = "01a03816-3b42-78d1-a818-65fdcb9e8a73"

	MarkCodexRootChannelAffinityUsed(c, "pro", "gpt-5.6-luna", 22, rootID)
	adminInfo := map[string]interface{}{}
	AppendChannelAffinityAdminInfo(c, adminInfo)

	affinity, ok := adminInfo["channel_affinity"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "Codex root session", affinity["rule_name"])
	require.Equal(t, "pro", affinity["selected_group"])
	require.Equal(t, "gpt-5.6-luna", affinity["model"])
	require.Equal(t, 22, affinity["channel_id"])
	require.Equal(t, []int{22}, affinity["channel_pool"])
	require.Equal(t, "verified_root_session", affinity["key_source"])
	require.Equal(t, "root_session_id", affinity["key_key"])
	require.NotEmpty(t, affinity["key_hint"])
	require.NotEmpty(t, affinity["key_fp"])
	require.NotContains(t, affinity["key_hint"], rootID)
	require.Equal(t, affinityFingerprint(rootID), affinity["key_fp"])
}

func TestMarkCodexRootChannelAffinityUsedRecordsUsageCacheStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	const (
		rootID     = "01a03816-3b42-78d1-a818-65fdcb9e8a74"
		ruleName   = "Codex root session"
		usingGroup = "gpt-pro"
	)
	keyFP := affinityFingerprint(rootID)
	entryKey := channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFP)
	statsCache := getChannelAffinityUsageCacheStatsCache()
	_, _ = statsCache.DeleteMany([]string{entryKey})
	t.Cleanup(func() {
		_, _ = statsCache.DeleteMany([]string{entryKey})
	})

	MarkCodexRootChannelAffinityUsed(c, usingGroup, "gpt-5.6-luna", 22, rootID)
	_, _, hasAffinityCache := getChannelAffinityContext(c)
	require.False(t, hasAffinityCache)
	_, hasAffinityMeta := getChannelAffinityMeta(c)
	require.False(t, hasAffinityMeta)

	usage := &dto.Usage{
		PromptTokens:     17645,
		CompletionTokens: 294,
		TotalTokens:      17939,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 4864,
		},
	}
	ObserveChannelAffinityUsageCacheByRelayFormat(c, usage, types.RelayFormatOpenAIResponses)

	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)
	require.Equal(t, ruleName, stats.RuleName)
	require.Equal(t, usingGroup, stats.UsingGroup)
	require.Equal(t, keyFP, stats.KeyFingerprint)
	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 1, stats.Hit)
	require.EqualValues(t, 17645, stats.PromptTokens)
	require.EqualValues(t, 4864, stats.CachedTokens)
	require.EqualValues(t, 294, stats.CompletionTokens)
	require.EqualValues(t, 17939, stats.TotalTokens)
	require.Positive(t, stats.WindowSeconds)
	require.Positive(t, stats.LastSeenAt)
}

func TestMarkCodexRootChannelAffinityUsedDoesNotOverwriteRuleInfo(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	original := map[string]interface{}{
		"rule_name":  "Configured rule",
		"channel_id": 7,
	}
	c.Set(ginKeyChannelAffinityLogInfo, original)

	MarkCodexRootChannelAffinityUsed(c, "pro", "gpt-5.6-luna", 22, "01a03816-3b42-78d1-a818-65fdcb9e8a73")
	adminInfo := map[string]interface{}{}
	AppendChannelAffinityAdminInfo(c, adminInfo)

	require.Equal(t, original, adminInfo["channel_affinity"])
	require.Equal(t, "Configured rule", original["rule_name"])
	require.Equal(t, 7, original["channel_id"])
}

func TestMarkCodexRootChannelAffinityUsedRejectsIncompleteIdentity(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	MarkCodexRootChannelAffinityUsed(c, "pro", "gpt-5.6-luna", 22, "")
	adminInfo := map[string]interface{}{}
	AppendChannelAffinityAdminInfo(c, adminInfo)
	require.NotContains(t, adminInfo, "channel_affinity")
}
