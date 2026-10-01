package controller

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfficialPricesExcludeResellersAndPreserveNumbers(t *testing.T) {
	prices, err := parseOfficialModelPrices(strings.NewReader(`{
		"deepseek":{"models":{"deepseek-v4-flash":{"modalities":{"output":["text"]},"cost":{"input":10,"output":20,"cache_read":0,"cache_write":3,"reasoning":20}}}},
		"deepinfra":{"models":{"deepseek-v4-flash":{"modalities":{"output":["text"]},"cost":{"input":1,"output":2}}}},
		"alibaba-cn":{"models":{"deepseek-v4-flash":{"modalities":{"output":["text"]},"cost":{"input":2,"output":3}}}}
	}`))
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, "deepseek", prices[0].Provider)
	assert.Equal(t, map[string]float64{"input": 10, "output": 20, "cache_read": 0, "cache_write": 3}, prices[0].Cost)
}

func TestOfficialPricesRejectUnsupportedOrInvalidCosts(t *testing.T) {
	for _, cost := range []string{`{}`, `{"input":0,"output":2}`, `{"input":-1,"output":2}`, `{"input":1}`, `{"input":1,"output":2,"cache_read":null}`} {
		t.Run(cost, func(t *testing.T) {
			_, err := parseOfficialModelPrices(strings.NewReader(`{"deepseek":{"models":{"deepseek-test":{"modalities":{"output":["text"]},"cost":` + cost + `}}}}`))
			require.Error(t, err)
		})
	}
	_, err := parseOfficialModelPrices(strings.NewReader(`<html>Login</html>`))
	require.ErrorContains(t, err, "invalid JSON")
}

func TestOfficialPricesConvertContextTierSources(t *testing.T) {
	prices, err := parseOfficialModelPrices(strings.NewReader(`{"minimax":{"models":{"MiniMax-M3":{"modalities":{"output":["text"]},"cost":{"input":0.3,"output":1.2,"tiers":[{"input":0.6,"output":2.4,"tier":{"type":"context","size":512000}}]}}}}}`))
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.False(t, prices[0].ManualOnly)
	assert.Contains(t, prices[0].Expression, "len > 512000")
	assert.Equal(t, 0.3, prices[0].Cost["input"])
}

func TestOfficialPricesFiveVendorSnapshot(t *testing.T) {
	// Minimal original-provider entries captured from models.dev/api.json.
	file, err := os.Open("testdata/official_pricing_five_vendors.json")
	require.NoError(t, err)
	defer file.Close()
	prices, err := parseOfficialModelPrices(file)
	require.NoError(t, err)
	byID := map[string]officialModelPrice{}
	for _, price := range prices {
		assert.False(t, price.ManualOnly, "%s/%s", price.Provider, price.Model)
		byID[price.Provider+"/"+price.Model] = price
	}
	for id, input := range map[string]float64{
		"deepseek/deepseek-v4-flash": 0.15,
		"deepseek/deepseek-v4-pro":   0.435,
		"moonshotai/kimi-k2.6":       0.95,
		"moonshotai-cn/kimi-k2.6":    0.95,
		"zai/glm-5.3":                1.4,
		"zhipuai/glm-5.3":            1.4,
		"alibaba/qwen-flash":         0.05,
		"alibaba-cn/qwen-flash":      0.022,
		"minimax/MiniMax-M2.5":       0.3,
		"minimax-cn/MiniMax-M2.5":    0.3,
	} {
		require.Contains(t, byID, id)
		assert.Equal(t, input, byID[id].Cost["input"], id)
	}
	assert.Contains(t, byID["minimax/MiniMax-M3"].Expression, "len > 512000")
	assert.Contains(t, byID["alibaba-cn/qwen3.7-flash"].Expression, "len > 32000")
	assert.NotEqual(t, byID["alibaba/qwen3.7-flash"].Expression, byID["alibaba-cn/qwen3.7-flash"].Expression)
}

func TestOfficialPricesKeepRegionalAlternativesAndMissingCache(t *testing.T) {
	prices, err := parseOfficialModelPrices(strings.NewReader(`{
		"zai":{"models":{"glm-5.3":{"modalities":{"output":["text"]},"cost":{"input":1.4,"output":4.4}}}},
		"zhipuai":{"models":{"glm-5.3":{"modalities":{"output":["text"]},"cost":{"input":10,"output":20}}}}
	}`))
	require.NoError(t, err)
	require.Len(t, prices, 2)
	assert.NotEqual(t, prices[0].Cost["input"], prices[1].Cost["input"])
	assert.NotContains(t, prices[0].Cost, "cache_read")
}
