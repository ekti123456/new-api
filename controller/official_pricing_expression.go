package controller

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
)

type officialContextTier struct {
	Threshold float64
	Cost      map[string]float64
}

// Explicit tiers are authoritative. context_over_200k is a legacy compatibility
// projection which can have a different threshold (e.g. MiniMax M3 is 512000).
func officialPricingExpression(raw map[string]any, base map[string]float64) (string, error) {
	var tiers []officialContextTier
	if entries, exists := raw["tiers"]; exists {
		items, ok := entries.([]any)
		if !ok {
			return "", fmt.Errorf("invalid context tiers")
		}
		seen := map[float64]bool{}
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return "", fmt.Errorf("invalid context tier")
			}
			metadata, ok := entry["tier"].(map[string]any)
			if !ok {
				return "", fmt.Errorf("missing context tier metadata")
			}
			size, ok := metadata["size"].(float64)
			if !ok || size < 0 || size > 9007199254740991 || math.Trunc(size) != size || seen[size] || (metadata["type"] != nil && metadata["type"] != "context") {
				return "", fmt.Errorf("unsupported context tier")
			}
			seen[size] = true
			cost, err := officialTierCost(entry)
			if err != nil {
				return "", err
			}
			tiers = append(tiers, officialContextTier{size, cost})
		}
	} else if legacy, exists := raw["context_over_200k"]; exists {
		entry, ok := legacy.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid legacy context price")
		}
		cost, err := officialTierCost(entry)
		if err != nil {
			return "", err
		}
		tiers = append(tiers, officialContextTier{200000, cost})
	}
	if len(tiers) == 0 {
		return "", nil
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i].Threshold > tiers[j].Threshold })
	// Variable detection is global to the expression. A branch without a cache
	// price must charge those tokens at its input rate, not accidentally make them free.
	cacheRead, cacheWrite := false, false
	for _, tier := range append(tiers, officialContextTier{Cost: base}) {
		_, read := tier.Cost["cache_read"]
		_, write := tier.Cost["cache_write"]
		cacheRead = cacheRead || read
		cacheWrite = cacheWrite || write
	}
	parts := make([]string, 0, len(tiers)+1)
	for _, tier := range tiers {
		size := strconv.FormatFloat(tier.Threshold, 'f', -1, 64)
		parts = append(parts, "len > "+size+" ? tier(\"over_"+size+"\", "+officialTierFormula(tier.Cost, cacheRead, cacheWrite)+") : ")
	}
	parts = append(parts, "tier(\"base\", "+officialTierFormula(base, cacheRead, cacheWrite)+")")
	expression := strings.Join(parts, "")
	if _, err := billingexpr.CompileFromCache(expression); err != nil {
		return "", err
	}
	return expression, nil
}

func officialTierCost(raw map[string]any) (map[string]float64, error) {
	cost := make(map[string]float64)
	for key, rawValue := range raw {
		if key == "tier" {
			continue
		}
		if key != "input" && key != "output" && key != "cache_read" && key != "cache_write" && key != "reasoning" {
			return nil, fmt.Errorf("unsupported tier price dimension")
		}
		value, ok := rawValue.(float64)
		if !ok || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid tier price")
		}
		cost[key] = value
	}
	_, input := cost["input"]
	_, output := cost["output"]
	if !input || !output {
		return nil, fmt.Errorf("missing tier price")
	}
	if reasoning, exists := cost["reasoning"]; exists && reasoning != cost["output"] {
		return nil, fmt.Errorf("separate reasoning pricing is unsupported")
	}
	delete(cost, "reasoning")
	return cost, nil
}

func officialTierFormula(cost map[string]float64, cacheRead, cacheWrite bool) string {
	format := func(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
	formula := "p * " + format(cost["input"]) + " + c * " + format(cost["output"])
	for _, field := range []struct {
		key, variable string
		used          bool
	}{{"cache_read", "cr", cacheRead}, {"cache_write", "cc", cacheWrite}} {
		if !field.used {
			continue
		}
		price, exists := cost[field.key]
		if !exists {
			price = cost["input"]
		}
		formula += " + " + field.variable + " * " + format(price)
	}
	return formula
}
