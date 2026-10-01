package controller

import (
	"fmt"
	"io"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

var cnyVendorPrefixes = map[string][]string{
	"alibaba": {"qwen", "qwq-", "qvq-"}, "moonshot": {"kimi-", "moonshot-"},
	"deepseek": {"deepseek-"}, "minimax": {"minimax-"}, "zhipu": {"glm-"},
}
var modelPriceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

type abacusCost struct {
	Input     *float64 `json:"inputPriceOrig"`
	Output    *float64 `json:"outputPriceOrig"`
	CacheRead *float64 `json:"cachedInputPriceOrig"`
}
type abacusTier struct {
	abacusCost
	UpTo *float64 `json:"upTo"`
}

func (cost abacusCost) prices() (map[string]float64, error) {
	if cost.Input == nil || cost.Output == nil {
		return nil, fmt.Errorf("missing original currency prices")
	}
	values := map[string]float64{"input": *cost.Input, "output": *cost.Output}
	if cost.CacheRead != nil {
		values["cache_read"] = *cost.CacheRead
	}
	for _, number := range values {
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
			return nil, fmt.Errorf("invalid original currency price")
		}
	}
	return values, nil
}

func parseAbacusCNYPrices(reader io.Reader) ([]officialModelPrice, error) {
	var data struct {
		Meta struct {
			Unit string `json:"unit"`
		} `json:"meta"`
		Vendors []struct {
			ID       string `json:"id"`
			Currency string `json:"currency"`
		} `json:"vendors"`
		Models []struct {
			abacusCost
			ID           string       `json:"id"`
			Name         string       `json:"name"`
			Vendor       string       `json:"vendorId"`
			Currency     string       `json:"priceCurrency"`
			Retired      bool         `json:"retired"`
			Modalities   []string     `json:"modality"`
			Tiers        []abacusTier `json:"tiers"`
			Source       string       `json:"source"`
			LastVerified string       `json:"lastVerified"`
			TimePricing  *struct {
				Timezone string `json:"timezone"`
				Bands    []struct {
					abacusCost
					ID       string `json:"id"`
					Schedule string `json:"schedule"`
				} `json:"bands"`
			} `json:"timePricing"`
		} `json:"models"`
	}
	if err := common.DecodeJson(reader, &data); err != nil {
		return nil, fmt.Errorf("invalid LLM Abacus response")
	}
	if data.Meta.Unit != "per_million_tokens" {
		return nil, fmt.Errorf("unsupported LLM Abacus pricing unit")
	}
	currencies := map[string]string{}
	for _, vendor := range data.Vendors {
		currencies[vendor.ID] = vendor.Currency
	}
	prices := []officialModelPrice{}
	for _, model := range data.Models {
		prefixes, owned := cnyVendorPrefixes[model.Vendor]
		currency := model.Currency
		if currency == "" {
			currency = currencies[model.Vendor]
		}
		if !owned || currency != "CNY" || model.Retired {
			continue
		}
		// The feed uses slug IDs (kimi-k2-6); its display name retains the
		// decimal version (Kimi K2.6). Accept this mapping only if slugs agree.
		name := strings.ToLower(strings.Join(strings.Fields(model.Name), "-"))
		if !modelPriceNamePattern.MatchString(name) || strings.ReplaceAll(name, ".", "-") != model.ID {
			name = model.ID
		}
		belongs := false
		for _, prefix := range prefixes {
			belongs = belongs || strings.HasPrefix(name, prefix)
		}
		if !belongs {
			continue
		}
		cost, err := model.abacusCost.prices()
		if err != nil || cost["input"] <= 0 {
			continue
		}
		price := officialModelPrice{Provider: model.Vendor, Model: name, Cost: cost, Currency: "CNY", Source: "LLM Abacus", SourceURL: safePricingSourceURL(model.Source), VerifiedAt: model.LastVerified}
		if model.TimePricing != nil {
			bands := map[string]map[string]float64{}
			schedules := map[string]string{"peak": "09:00-12:00、14:00-18:00", "off_peak": "00:00-09:00、12:00-14:00、18:00-24:00"}
			supported := model.TimePricing.Timezone == "Asia/Shanghai" && len(model.TimePricing.Bands) == 2 && len(model.Tiers) == 0
			for _, band := range model.TimePricing.Bands {
				supported = supported && schedules[band.ID] != "" && schedules[band.ID] == band.Schedule && bands[band.ID] == nil
				values, err := band.abacusCost.prices()
				if err == nil {
					bands[band.ID] = values
				}
			}
			if supported && model.Vendor == "deepseek" && bands["peak"] != nil && bands["off_peak"] != nil {
				price.Cost = bands["off_peak"]
				price.Expression = deepSeekTimeExpression(bands["peak"], bands["off_peak"])
			} else {
				price.ManualOnly = true
			}
		} else if len(model.Tiers) > 0 {
			price.Expression, err = abacusTierExpression(model.Tiers, cost)
			price.ManualOnly = err != nil
		}
		prices = append(prices, price)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no original CNY prices in LLM Abacus response")
	}
	return prices, nil
}

func abacusTierExpression(tiers []abacusTier, base map[string]float64) (string, error) {
	if len(tiers) < 2 {
		return "", fmt.Errorf("incomplete tier pricing")
	}
	var converted []any
	threshold := float64(0)
	for index, tier := range tiers {
		cost, err := tier.abacusCost.prices()
		if err != nil {
			return "", err
		}
		if index == 0 {
			if !maps.Equal(cost, base) {
				return "", fmt.Errorf("base price does not match first tier")
			}
		} else {
			row := map[string]any{"tier": map[string]any{"type": "context", "size": threshold}}
			for key, value := range cost {
				row[key] = value
			}
			converted = append(converted, row)
		}
		if index == len(tiers)-1 {
			if tier.UpTo != nil {
				return "", fmt.Errorf("missing final unbounded tier")
			}
		} else {
			if tier.UpTo == nil || *tier.UpTo <= threshold || math.IsInf(*tier.UpTo, 0) || math.Trunc(*tier.UpTo) != *tier.UpTo || *tier.UpTo > 9007199254740991 {
				return "", fmt.Errorf("invalid tier boundary")
			}
			threshold = *tier.UpTo
		}
	}
	return officialPricingExpression(map[string]any{"tiers": converted}, base)
}

func safePricingSourceURL(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

func parseModelsCNDeepSeekPrices(reader io.Reader) ([]officialModelPrice, error) {
	var data struct {
		SchemaVersion string `json:"schemaVersion"`
		Providers     []struct {
			ID     string `json:"id"`
			Health struct {
				Status           string `json:"status"`
				LastSuccessfulAt string `json:"lastSuccessfulAt"`
			} `json:"health"`
			Models []struct {
				ID     string `json:"id"`
				Prices []struct {
					Market        string `json:"market"`
					Currency      string `json:"currency"`
					Unit          string `json:"unit"`
					RateType      string `json:"rateType"`
					EffectiveFrom string `json:"effectiveFrom"`
					Input         struct {
						Standard *float64 `json:"standard"`
						CacheHit *float64 `json:"cacheHit"`
					} `json:"input"`
					Output         *float64 `json:"output"`
					SourceURL      string   `json:"sourceUrl"`
					DailyTimeRange struct {
						Label     string                  `json:"label"`
						TimeZone  string                  `json:"timeZone"`
						Intervals []deepSeekPriceInterval `json:"intervals"`
					} `json:"dailyTimeRange"`
				} `json:"prices"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := common.DecodeJson(reader, &data); err != nil {
		return nil, fmt.Errorf("invalid models-cn response")
	}
	if data.SchemaVersion != "1.0" {
		return nil, fmt.Errorf("unsupported models-cn schema")
	}
	prices := []officialModelPrice{}
	for _, provider := range data.Providers {
		if provider.ID != "deepseek" || provider.Health.Status != "healthy" {
			continue
		}
		for _, model := range provider.Models {
			if !strings.HasPrefix(model.ID, "deepseek-") {
				continue
			}
			bands := map[string]map[string]float64{}
			supported := true
			sourceURL := ""
			for _, row := range model.Prices {
				if row.Market != "china" || row.Currency != "CNY" || row.Unit != "1M_tokens" || row.RateType != "standard" || row.DailyTimeRange.TimeZone != "Asia/Shanghai" {
					continue
				}
				if row.EffectiveFrom != "" {
					effective, err := time.Parse(time.RFC3339, row.EffectiveFrom)
					if err != nil || effective.After(time.Now()) {
						continue
					}
				}
				if !supportedDeepSeekIntervals(row.DailyTimeRange.Label, row.DailyTimeRange.Intervals) {
					supported = false
					continue
				}
				cost, err := (abacusCost{Input: row.Input.Standard, Output: row.Output, CacheRead: row.Input.CacheHit}).prices()
				if err != nil {
					continue
				}
				switch row.DailyTimeRange.Label {
				case "高峰时段":
					supported = supported && bands["peak"] == nil
					bands["peak"] = cost
				case "空闲时段":
					supported = supported && bands["off_peak"] == nil
					bands["off_peak"] = cost
				}
				sourceURL = safePricingSourceURL(row.SourceURL)
			}
			if !supported || bands["peak"] == nil || bands["off_peak"] == nil {
				continue
			}
			prices = append(prices, officialModelPrice{Provider: "deepseek", Model: model.ID, Cost: bands["off_peak"], Currency: "CNY", Source: "models-cn", SourceURL: sourceURL, VerifiedAt: provider.Health.LastSuccessfulAt, Expression: deepSeekTimeExpression(bands["peak"], bands["off_peak"])})
		}
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no complete DeepSeek CNY time bands in models-cn response")
	}
	return prices, nil
}

type deepSeekPriceInterval struct {
	Start string   `json:"start"`
	End   string   `json:"end"`
	Days  []string `json:"days"`
}

// cn_off_peak implements these official bands. Do not silently import changed
// source schedules into an expression with different runtime semantics.
func supportedDeepSeekIntervals(label string, intervals []deepSeekPriceInterval) bool {
	windows := []string{}
	switch label {
	case "高峰时段":
		windows = []string{"09:00-12:00", "14:00-18:00"}
	case "空闲时段":
		windows = []string{"00:00-09:00", "12:00-14:00", "18:00-00:00"}
	default:
		return false
	}
	expected := []string{}
	for _, day := range []string{"mon", "tue", "wed", "thu", "fri"} {
		for _, window := range windows {
			expected = append(expected, day+":"+window)
		}
	}
	if label == "空闲时段" {
		expected = append(expected, "sat:00:00-00:00", "sun:00:00-00:00")
	}
	actual := []string{}
	for _, interval := range intervals {
		for _, day := range interval.Days {
			actual = append(actual, day+":"+interval.Start+"-"+interval.End)
		}
	}
	slices.Sort(expected)
	slices.Sort(actual)
	return slices.Equal(expected, actual)
}

func deepSeekTimeExpression(peak, offPeak map[string]float64) string {
	_, readPeak := peak["cache_read"]
	_, readOff := offPeak["cache_read"]
	read := readPeak || readOff
	return "cn_off_peak() ? tier(\"off_peak\", " + officialTierFormula(offPeak, read, false) + ") : tier(\"peak\", " + officialTierFormula(peak, read, false) + ")"
}
