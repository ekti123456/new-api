package service

import (
	"encoding/base64"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTieredSettlementLogKeepsActualRequestRuleMatches(test *testing.T) {
	expression := `tier("base", p * 10) * (param("model") == "gpt-6-astra" ? 1.6 : 1) * (param("service_tier") == "priority" ? 2 : 1)`
	info := &relaycommon.RelayInfo{
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{"model":"gpt-6-astra"}`)},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression),
			GroupRatio: 1, QuotaPerUnit: 1000000, EstimatedTier: "base",
		},
	}
	applied, quota, result := TryTieredSettle(info, billingexpr.TokenParams{P: 1000, Len: 1000})
	require.True(test, applied)
	require.NotNil(test, result)
	assert.Equal(test, 16000, quota)
	info.BillingRequestInput.Body = []byte(`{"model":"different","service_tier":"priority"}`)
	other := map[string]interface{}{}
	InjectTieredBillingInfo(other, info, result)
	diagnostic := other["admin_info"].(map[string]interface{})["billing_request"].(*relaycommon.BillingRequestDiagnostic)
	assert.Equal(test, "absent", diagnostic.ServiceTier.State)
	assert.Nil(test, diagnostic.ServiceTier.Value, "must describe the evaluated input, not the later replacement")
	encoded, err := common.Marshal(other)
	require.NoError(test, err)
	var stored struct {
		Expression  string                         `json:"expr_b64"`
		MatchedTier string                         `json:"matched_tier"`
		Matches     []billingexpr.RequestRuleMatch `json:"request_rule_matches"`
	}
	require.NoError(test, common.Unmarshal(encoded, &stored))
	assert.Equal(test, base64.StdEncoding.EncodeToString([]byte(expression)), stored.Expression)
	assert.Equal(test, "base", stored.MatchedTier)
	assert.Equal(test, []billingexpr.RequestRuleMatch{
		{Expression: `param("model") == "gpt-6-astra" ? 1.6 : 1`, Matched: true},
		{Expression: `param("service_tier") == "priority" ? 2 : 1`, Matched: false},
	}, stored.Matches)

	failedSettlement := map[string]interface{}{}
	InjectTieredBillingInfo(failedSettlement, info, nil)
	assert.NotContains(test, failedSettlement, "request_rule_matches")
}

func TestTieredCodexBillingPriorityFallbackPreservesOriginalAndAppliesOnce(t *testing.T) {
	expression := `tier("gpt", p * 5 + c * 30 + cr * 0.5 + cc * 6.25) * (param("service_tier") == "priority" ? 2 : 1)`
	for _, tc := range []struct {
		name, body, reported, source string
		matched                      bool
	}{
		{"original priority", `{"service_tier":"priority"}`, "", "request", true},
		{"original wins downgrade", `{"service_tier":"priority"}`, "default", "request", true},
		{"both priority only once", `{"service_tier":"priority"}`, "priority", "request", true},
		{"missing fallback", `{}`, "priority", "codex2api", true},
		{"fast fallback", `{"service_tier":"fast"}`, "priority", "codex2api", true},
		{"empty capture fallback", ``, "priority", "codex2api", true},
		{"no report", `{}`, "", "none", false},
		{"default report", `{}`, "default", "none", false},
		{"fast alone unchanged", `{"service_tier":"fast"}`, "", "none", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				BillingRequestInput:   &billingexpr.RequestInput{Body: []byte(tc.body)},
				TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), GroupRatio: 0.2, QuotaPerUnit: 500000},
			}
			if tc.reported != "" {
				info.CodexBilling = &relaycommon.CodexBillingObservation{}
				info.CodexBilling.Record(relaycommon.CodexBillingReport{ServiceTier: tc.reported, Source: "upstream_response", ActualServiceTier: tc.reported})
			}
			applied, quota, result := TryTieredSettle(info, billingexpr.TokenParams{P: 68, C: 3673, CR: 106870, CC: 520, Len: 107458})
			require.True(t, applied)
			require.NotNil(t, result)
			want := 16722
			if tc.matched {
				want = 33443
			}
			require.Equal(t, want, quota)
			require.Equal(t, tc.body, string(info.BillingRequestInput.Body), "do not replace or mutate original input")
			require.Equal(t, tc.source, info.BillingRequestDiagnostic.PriorityMatchSource)
			original := relaycommon.CaptureBillingRequestDiagnostic(info.BillingRequestInput)
			require.Equal(t, original.ServiceTier, info.BillingRequestDiagnostic.ServiceTier)
			other := map[string]interface{}{}
			InjectTieredBillingInfo(other, info, result)
			data, err := common.Marshal(other)
			require.NoError(t, err)
			var logged struct {
				Matches []billingexpr.RequestRuleMatch `json:"request_rule_matches"`
			}
			require.NoError(t, common.Unmarshal(data, &logged))
			require.Len(t, logged.Matches, 1)
			require.Equal(t, tc.matched, logged.Matches[0].Matched)
		})
	}
}
