package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const abacusPricingURL = "https://www.llmabacus.com/api/prices"
const modelsCNPricingURL = "https://null-object-0000.github.io/models-cn/v1/api.json"
const modelsDevPricingURL = "https://models.dev/api.json"

func fetchOfficialPriceSource(ctx context.Context, address string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricing source returned HTTP %d", response.StatusCode)
	}
	const limit = 20 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("pricing source response too large")
	}
	return body, nil
}

func loadOfficialPriceCatalog(ctx context.Context, fetch func(context.Context, string) ([]byte, error)) ([]officialModelPrice, []string, error) {
	sources := []struct {
		url     string
		parse   func(io.Reader) ([]officialModelPrice, error)
		warning string
	}{
		{abacusPricingURL, parseAbacusCNYPrices, "LLM Abacus prices are unavailable. Domestic USD prices will not be substituted."},
		{modelsCNPricingURL, parseModelsCNDeepSeekPrices, "models-cn prices are unavailable. DeepSeek uses verified LLM Abacus time bands when available."},
		{modelsDevPricingURL, parseOfficialModelPrices, "models.dev prices are unavailable. International prices were not loaded."},
	}
	results := make([][]officialModelPrice, len(sources))
	errors := make([]error, len(sources))
	var group sync.WaitGroup
	for index, source := range sources {
		group.Add(1)
		go func() {
			defer group.Done()
			body, err := fetch(ctx, source.url)
			if err != nil {
				errors[index] = err
				return
			}
			results[index], errors[index] = source.parse(bytes.NewReader(body))
		}()
	}
	group.Wait()
	warnings := []string{}
	for i, err := range errors {
		if err != nil {
			warnings = append(warnings, sources[i].warning)
		}
	}
	// Only these international manufacturers use models.dev USD prices. Chinese
	// providers and their international mirrors cannot enter through this path.
	international := map[string]bool{"openai": true, "anthropic": true, "google": true, "xai": true, "mistral": true}
	prices := []officialModelPrice{}
	deepseekOverrides := map[string]bool{}
	for _, price := range results[1] {
		deepseekOverrides[price.Model] = true
	}
	for _, price := range results[0] {
		if price.Provider != "deepseek" || !deepseekOverrides[price.Model] {
			prices = append(prices, price)
		}
	}
	prices = append(prices, results[1]...)
	for _, price := range results[2] {
		if international[price.Provider] {
			prices = append(prices, price)
		}
	}
	sort.Slice(prices, func(i, j int) bool {
		left, right := prices[i], prices[j]
		if left.Model != right.Model {
			return left.Model < right.Model
		}
		return left.Provider < right.Provider
	})
	if len(prices) == 0 {
		return nil, warnings, fmt.Errorf("No usable pricing source is available. Existing prices were not changed.")
	}
	return prices, warnings, nil
}
