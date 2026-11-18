package service

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeLocalEstimateBillingSource(t *testing.T) {
	usage := &dto.Usage{BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 100, OutputTokens: 10})}
	usage.BillingUsage.Estimated = true
	assert.Equal(t, usageBillingPathLocal, usageBillingPathForLog(true, usage))
	assert.Equal(t, usageBillingPathAnthropicEstimated, usageBillingPathForLog(false, usage), "upstream estimates retain their separate provenance")
	usage.BillingUsage.Estimated = false
	assert.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(true, usage), "an unused fallback must not relabel measured upstream usage")
}

func TestClaudeCacheOnlyUsageIsBillable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name  string
		usage *dto.ClaudeUsage
		quota int
	}{
		{"cache read", &dto.ClaudeUsage{CacheReadInputTokens: 5000}, 500},
		{"cache write", &dto.ClaudeUsage{CacheCreationInputTokens: 1000}, 1250},
		{"split cache write", &dto.ClaudeUsage{CacheCreationInputTokens: 1000, CacheCreation: &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 600, Ephemeral1hInputTokens: 400}}, 1550},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, OriginModelName: "claude-opus-5-5", StartTime: time.Now(), PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 5, CacheRatio: 0.1, CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			usage := &dto.Usage{BillingUsage: dto.NewClaudeMessagesBillingUsage(tc.usage)}
			summary := calculateTextQuotaSummary(ctx, info, effectiveBillingUsage(usage))
			assert.True(t, summary.hasBillableUsage())
			assert.Equal(t, tc.quota, summary.Quota)
		})
	}
}

func TestClaudeToolResultImageEstimateMatchesDirectImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousCount, previousMedia := constant.CountToken, constant.GetMediaToken
	constant.CountToken, constant.GetMediaToken = true, false
	t.Cleanup(func() { constant.CountToken, constant.GetMediaToken = previousCount, previousMedia })
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	payload := strings.Repeat("a0Z9+/b1", 250000)
	block := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": payload}}
	direct := &dto.ClaudeRequest{Model: "claude-opus-5-5", Messages: []dto.ClaudeMessage{{Role: "user", Content: []any{block}}}}
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, direct.Model)
	nested := &dto.ClaudeRequest{Model: direct.Model, Messages: []dto.ClaudeMessage{{Role: "user", Content: []any{map[string]any{"type": "tool_result", "tool_use_id": "tool_1", "content": []any{block}}}}}}
	info := &relaycommon.RelayInfo{OriginModelName: direct.Model, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: direct.Model}}
	want, err := EstimateRequestToken(ctx, direct.GetTokenCountMeta(), info)
	require.NoError(t, err)
	got, err := EstimateRequestToken(ctx, nested.GetTokenCountMeta(), info)
	require.NoError(t, err)
	assert.Positive(t, got)
	assert.Equal(t, want, got)
	assert.Less(t, got, 2000, "media must not become millions of text tokens")
}
