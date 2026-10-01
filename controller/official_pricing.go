package controller

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// Provider ownership is deliberately explicit: a manufacturer may also resell
// other manufacturers' models. Coding plans and aggregators are not price sources.
var officialModelPrefixes = map[string][]string{
	"deepseek": {"deepseek-"},
	"zai":      {"glm-"}, "zhipuai": {"glm-"},
	"moonshotai": {"kimi-", "moonshot-"}, "moonshotai-cn": {"kimi-", "moonshot-"},
	"alibaba": {"qwen", "qwq-", "qvq-"}, "alibaba-cn": {"qwen", "qwq-", "qvq-"},
	"minimax": {"minimax-"}, "minimax-cn": {"minimax-"},
	"stepfun":   {"step-"},
	"openai":    {"gpt-", "chatgpt-", "o1", "o3", "o4", "codex-"},
	"anthropic": {"claude-"}, "google": {"gemini-", "gemma-"}, "xai": {"grok-"},
	"mistral": {"mistral-", "ministral-", "magistral-", "codestral-", "devstral-", "pixtral-"},
}

type officialModelPrice struct {
	Provider   string             `json:"provider"`
	Model      string             `json:"model"`
	Cost       map[string]float64 `json:"cost"`
	ManualOnly bool               `json:"manual_only,omitempty"`
	Expression string             `json:"expression,omitempty"`
	Currency   string             `json:"currency,omitempty"`
	Source     string             `json:"source,omitempty"`
	SourceURL  string             `json:"source_url,omitempty"`
	VerifiedAt string             `json:"verified_at,omitempty"`
}

func parseOfficialModelPrices(reader io.Reader) ([]officialModelPrice, error) {
	var providers map[string]struct {
		Models map[string]struct {
			Cost       map[string]any `json:"cost"`
			Modalities struct {
				Output []string `json:"output"`
			} `json:"modalities"`
		} `json:"models"`
	}
	if err := common.DecodeJson(reader, &providers); err != nil {
		return nil, fmt.Errorf("models.dev returned invalid JSON")
	}
	prices := make([]officialModelPrice, 0)
	for provider, prefixes := range officialModelPrefixes {
		for model, data := range providers[provider].Models {
			name := strings.ToLower(model)
			owned := false
			for _, prefix := range prefixes {
				owned = owned || strings.HasPrefix(name, prefix)
			}
			if !owned || !slices.Contains(data.Modalities.Output, "text") || len(data.Modalities.Output) != 1 {
				continue
			}
			cost := make(map[string]float64)
			valid := true
			manualOnly := false
			for key, raw := range data.Cost {
				switch key {
				case "input", "output", "cache_read", "cache_write":
					value, ok := raw.(float64)
					if !ok || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
						valid = false
						continue
					}
					cost[key] = value
				case "reasoning":
					// Reasoning tokens are billed at the output rate by this editor.
					value, ok := raw.(float64)
					output, outputOK := data.Cost["output"].(float64)
					if !ok || !outputOK || value != output {
						manualOnly = true
					}
				case "tiers", "context_over_200k":
					// Converted below using the source's explicit thresholds.
				default:
					// Do not silently flatten context tiers, audio/image prices, or
					// new dimensions that the per-token editor cannot represent.
					manualOnly = true
				}
			}
			_, outputPresent := cost["output"]
			if !valid || cost["input"] <= 0 || !outputPresent {
				continue
			}
			expression, err := officialPricingExpression(data.Cost, cost)
			if err != nil {
				manualOnly = true
			}
			prices = append(prices, officialModelPrice{Provider: provider, Model: model, Cost: cost, ManualOnly: manualOnly, Expression: expression, Currency: "USD", Source: "models.dev"})
		}
	}
	sort.Slice(prices, func(i, j int) bool {
		if prices[i].Model == prices[j].Model {
			return prices[i].Provider < prices[j].Provider
		}
		return prices[i].Model < prices[j].Model
	})
	if len(prices) == 0 {
		return nil, fmt.Errorf("models.dev contains no supported official token prices")
	}
	return prices, nil
}

// GetOfficialModelPrices keeps domestic original CNY prices separate from
// international USD prices. A failed CNY source never falls back to USD.
func GetOfficialModelPrices(c *gin.Context) {
	prices, warnings, err := loadOfficialPriceCatalog(c.Request.Context(), fetchOfficialPriceSource)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": prices, "warnings": warnings})
}
