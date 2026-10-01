package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCNYPricesUseOriginalNumbersAndRestoreVersionNames(t *testing.T) {
	prices, err := parseAbacusCNYPrices(strings.NewReader(`{"meta":{"unit":"per_million_tokens"},"vendors":[{"id":"moonshot","currency":"CNY"}],"models":[
	{"id":"kimi-k2-6","name":"Kimi K2.6","vendorId":"moonshot","inputPrice":99,"outputPrice":199,"inputPriceOrig":10,"outputPriceOrig":20,"cachedInputPriceOrig":0},
	{"id":"glm-5-3","name":"GLM-5.3","vendorId":"zhipu","priceCurrency":"USD","inputPriceOrig":8,"outputPriceOrig":28},
	{"id":"deepseek-v3-2-legacy","name":"DeepSeek V3.2","vendorId":"deepseek","priceCurrency":"CNY","retired":true,"inputPriceOrig":2,"outputPriceOrig":8},
	{"id":"minimax-m3","name":"MiniMax M3","vendorId":"minimax","priceCurrency":"CNY","inputPrice":2.1,"outputPrice":8.4}
	]}`))
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, "kimi-k2.6", prices[0].Model)
	assert.Equal(t, "CNY", prices[0].Currency)
	assert.Equal(t, map[string]float64{"input": 10, "output": 20, "cache_read": 0}, prices[0].Cost)
}

func TestCNYPricesTierBoundariesAndFiveVendorSnapshot(t *testing.T) {
	file, err := os.Open("testdata/official_pricing_abacus_cny.json")
	require.NoError(t, err)
	defer file.Close()
	prices, err := parseAbacusCNYPrices(file)
	require.NoError(t, err)
	byID := map[string]officialModelPrice{}
	for _, price := range prices {
		byID[price.Model] = price
		assert.Equal(t, "CNY", price.Currency)
		assert.False(t, price.ManualOnly, price.Model)
	}
	for name, want := range map[string]float64{"qwen3.5-flash": 0.2, "kimi-k2.6": 6.5, "minimax-m3": 2.1, "glm-5.3": 8, "deepseek-v4-pro": 4.5} {
		require.Contains(t, byID, name)
		assert.Equal(t, want, byID[name].Cost["input"], name)
	}
	for _, tc := range []struct {
		name   string
		length float64
		want   float64
	}{{"minimax-m3", 512000, 10.92}, {"minimax-m3", 512001, 21.84}, {"qwen3.5-flash", 128000, 2.2}, {"qwen3.5-flash", 128001, 8.8}, {"qwen3.5-flash", 256001, 13.2}} {
		actual, _, err := billingexpr.RunExpr(byID[tc.name].Expression, billingexpr.TokenParams{Len: tc.length, P: 1, C: 1, CR: 1})
		require.NoError(t, err)
		assert.InDelta(t, tc.want, actual, 1e-10, tc.name)
	}
}

func TestCNYPricesCatalogDoesNotFallbackToDomesticUSD(t *testing.T) {
	fetch := func(_ context.Context, address string) ([]byte, error) {
		if address != modelsDevPricingURL {
			return nil, fmt.Errorf("offline")
		}
		return []byte(`{"deepseek":{"models":{"deepseek-v4-pro":{"modalities":{"output":["text"]},"cost":{"input":0.435,"output":0.87}}}},"openai":{"models":{"gpt-test":{"modalities":{"output":["text"]},"cost":{"input":1,"output":2}}}}}`), nil
	}
	prices, warnings, err := loadOfficialPriceCatalog(context.Background(), fetch)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, "gpt-test", prices[0].Model)
	assert.Len(t, warnings, 2)
	_, _, err = loadOfficialPriceCatalog(context.Background(), func(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("offline") })
	require.Error(t, err)
}

func TestCNYPricesChangedTimeSchedulesCannotImportOldBillingRules(t *testing.T) {
	body, err := os.ReadFile("testdata/official_pricing_abacus_cny.json")
	require.NoError(t, err)
	prices, err := parseAbacusCNYPrices(strings.NewReader(strings.ReplaceAll(string(body), "09:00-12:00", "10:00-12:00")))
	require.NoError(t, err)
	for _, price := range prices {
		if price.Provider == "deepseek" {
			assert.True(t, price.ManualOnly)
			assert.Empty(t, price.Expression)
		}
	}
	body, err = os.ReadFile("testdata/official_pricing_deepseek_cny.json")
	require.NoError(t, err)
	_, err = parseModelsCNDeepSeekPrices(strings.NewReader(strings.ReplaceAll(string(body), "09:00", "10:00")))
	require.Error(t, err)
}

func TestCNYPricesNonuniformTimeDiscountKeepsSeparatePrices(t *testing.T) {
	expression := deepSeekTimeExpression(map[string]float64{"input": 9, "output": 27, "cache_read": 0.3}, map[string]float64{"input": 4.5, "output": 12, "cache_read": 0.15})
	at, err := time.Parse(time.RFC3339, "2026-10-01T10:00:00+08:00")
	require.NoError(t, err)
	cost, trace, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{P: 1, C: 1, CR: 1}, billingexpr.RequestInput{PricingTime: at})
	require.NoError(t, err)
	assert.InDelta(t, 16.65, cost, 1e-10)
	assert.Equal(t, "off_peak", trace.MatchedTier)
}

func TestCNYPricesModelsCNOverridesDeepSeekWithAutomaticTimeExpression(t *testing.T) {
	files := map[string]string{abacusPricingURL: "testdata/official_pricing_abacus_cny.json", modelsCNPricingURL: "testdata/official_pricing_deepseek_cny.json"}
	prices, _, err := loadOfficialPriceCatalog(context.Background(), func(_ context.Context, address string) ([]byte, error) {
		path, ok := files[address]
		if !ok {
			return nil, fmt.Errorf("offline")
		}
		return os.ReadFile(path)
	})
	require.NoError(t, err)
	var selected []officialModelPrice
	for _, price := range prices {
		if price.Model == "deepseek-v4-pro" {
			selected = append(selected, price)
		}
	}
	require.Len(t, selected, 1)
	assert.Equal(t, "models-cn", selected[0].Source)
	for _, tc := range []struct {
		at   string
		want float64
		tier string
	}{{"2026-09-30T10:00:00+08:00", 36.3, "peak"}, {"2026-10-01T10:00:00+08:00", 18.15, "off_peak"}, {"2026-10-10T10:00:00+08:00", 18.15, "off_peak"}} {
		at, err := time.Parse(time.RFC3339, tc.at)
		require.NoError(t, err)
		cost, trace, err := billingexpr.RunExprWithRequest(selected[0].Expression, billingexpr.TokenParams{P: 1, C: 1, CR: 1}, billingexpr.RequestInput{PricingTime: at})
		require.NoError(t, err)
		assert.InDelta(t, tc.want, cost, 1e-10)
		assert.Equal(t, "base", trace.MatchedTier)
		require.Len(t, trace.RequestRuleMatches, 1)
		assert.Equal(t, tc.tier == "off_peak", trace.RequestRuleMatches[0].Matched)
	}
	// Unknown schema versions must not be treated as a compatible CNY source.
	var malformed map[string]any
	body, err := os.ReadFile(files[modelsCNPricingURL])
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(body, &malformed))
	malformed["schemaVersion"] = "2.0"
	body, err = common.Marshal(malformed)
	require.NoError(t, err)
	_, err = parseModelsCNDeepSeekPrices(bytes.NewReader(body))
	require.Error(t, err)
}
