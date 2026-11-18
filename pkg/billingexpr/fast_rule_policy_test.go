package billingexpr

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The web synchronizer asserts its output against the same fixture. Exercise
// that saved expression with the real billing engine, including cache tokens
// and the exact long-context boundary.
func TestSyncedFastPolicyUsesOnlyOriginalRequestedTier(t *testing.T) {
	raw, err := os.ReadFile("testdata/fast-rule-policy.json")
	require.NoError(t, err)
	var policy struct {
		Expression string `json:"expression"`
	}
	require.NoError(t, common.Unmarshal(raw, &policy))
	for _, sample := range []struct {
		name, body string
		multiplier float64
	}{
		{"fast", `{"service_tier":"fast"}`, 2},
		{"priority", `{"service_tier":"priority"}`, 2},
		{"ultrafast", `{"service_tier":"ultrafast"}`, 6},
		{"absent", `{}`, 1},
		{"auto", `{"service_tier":"auto"}`, 1},
		{"default", `{"service_tier":"default"}`, 1},
		{"flex", `{"service_tier":"flex"}`, 1},
		{"wrong case", `{"service_tier":"Priority"}`, 1},
		{"camel case", `{"serviceTier":"priority"}`, 1},
		{"null", `{"service_tier":null}`, 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, context := range []struct {
				length, base float64
				tier         string
			}{{272000, 4000, "base"}, {272001, 7500, "long"}} {
				cost, trace, err := RunExprWithRequest(policy.Expression,
					TokenParams{P: 1000, C: 100, CR: 10000, Len: context.length},
					RequestInput{Body: []byte(sample.body), Headers: map[string]string{"OpenAI-Service-Tier": "ultrafast"}})
				require.NoError(t, err)
				assert.Equal(t, context.base*sample.multiplier, cost)
				assert.Equal(t, context.tier, trace.MatchedTier)
				matched := 0
				for _, rule := range trace.RequestRuleMatches {
					if rule.Matched {
						matched++
					}
				}
				if sample.multiplier == 1 {
					assert.Zero(t, matched)
				} else {
					assert.Equal(t, 1, matched, "acceleration must never multiply twice")
				}
			}
		})
	}
}
