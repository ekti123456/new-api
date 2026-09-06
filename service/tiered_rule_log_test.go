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
