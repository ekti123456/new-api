package controller

import (
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestOfficialPricesMiniMaxExplicitThresholdOverridesLegacyProjection(t *testing.T) {
	prices, err := parseOfficialModelPrices(strings.NewReader(`{"minimax":{"models":{"MiniMax-M3":{"modalities":{"output":["text"]},"cost":{"input":0.3,"output":1.2,"cache_read":0.06,"tiers":[{"input":0.6,"output":2.4,"cache_read":0.12,"tier":{"type":"context","size":512000}}],"context_over_200k":{"input":0.6,"output":2.4,"cache_read":0.12}}}}}}`))
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.False(t, prices[0].ManualOnly)
	for _, tc := range []struct {
		length, want float64
		tier         string
	}{{200001, 1.56, "base"}, {512000, 1.56, "base"}, {512001, 3.12, "over_512000"}} {
		cost, trace, err := billingexpr.RunExpr(prices[0].Expression, billingexpr.TokenParams{Len: tc.length, P: 1, C: 1, CR: 1})
		require.NoError(t, err)
		assert.InDelta(t, tc.want, cost, 1e-12)
		assert.Equal(t, tc.tier, trace.MatchedTier)
	}
}

func TestOfficialPricesQwenTiersPreserveBoundariesAndCacheTokens(t *testing.T) {
	prices, err := parseOfficialModelPrices(strings.NewReader(`{"alibaba-cn":{"models":{"qwen-test":{"modalities":{"output":["text"]},"cost":{"input":10,"output":20,"cache_read":0,"tiers":[{"input":30,"output":40,"cache_read":3,"tier":{"type":"context","size":256000}},{"input":15,"output":25,"tier":{"type":"context","size":128000}}]}}}}}`))
	require.NoError(t, err)
	require.Len(t, prices, 1)
	for _, tc := range []struct{ length, want float64 }{{128000, 30}, {128001, 55}, {256000, 55}, {256001, 73}} {
		cost, _, err := billingexpr.RunExpr(prices[0].Expression, billingexpr.TokenParams{Len: tc.length, P: 1, C: 1, CR: 1})
		require.NoError(t, err)
		assert.Equal(t, tc.want, cost)
	}
}

func TestOfficialPricesMalformedTiersCannotBecomeFlatPrices(t *testing.T) {
	for _, tier := range []string{`{"input":1,"output":2,"tier":{"type":"output","size":32000}}`, `{"input":1,"output":2,"tier":{"size":-1}}`, `{"input":1,"tier":{"size":32000}}`} {
		prices, err := parseOfficialModelPrices(strings.NewReader(`{"alibaba":{"models":{"qwen-test":{"modalities":{"output":["text"]},"cost":{"input":1,"output":2,"tiers":[` + tier + `]}}}}}`))
		require.NoError(t, err)
		require.Len(t, prices, 1)
		assert.True(t, prices[0].ManualOnly)
	}
}

func TestOfficialPricesUpdateRejectsStaleOrInvalidValues(t *testing.T) {
	current := map[string]any{"model_ratio": map[string]float64{"qwen": 5}}
	valid := officialPricingUpdate{Before: map[string]string{"ModelRatio": `{"qwen":5}`}, Values: map[string]string{"ModelRatio": `{"qwen":10}`}}
	require.NoError(t, validateOfficialPricingUpdate(valid, current))
	valid.Before["ModelRatio"] = `{"qwen":4}`
	require.ErrorContains(t, validateOfficialPricingUpdate(valid, current), "changed since preview")
	valid.Before["ModelRatio"] = `{"qwen":5}`
	valid.Values["ModelRatio"] = `{"qwen":-1}`
	require.Error(t, validateOfficialPricingUpdate(valid, current))
	require.Error(t, validateOfficialPricingUpdate(officialPricingUpdate{Values: map[string]string{"Token": "secret"}}, current))
	require.Error(t, validateOfficialPricingUpdate(officialPricingUpdate{Before: map[string]string{"billing_setting.billing_expr": "{}"}, Values: map[string]string{"billing_setting.billing_expr": `{"qwen":"p ** invalid"}`}}, current))
}
