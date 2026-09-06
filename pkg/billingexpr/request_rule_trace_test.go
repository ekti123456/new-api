package billingexpr

import (
	"testing"

	"github.com/expr-lang/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestRuleTracePreservesCostAndCapturesEachDecision(test *testing.T) {
	expression := `(len > 272000 && channel_id == 42 ? tier("272k", p * 20 + c * 75) : tier("gpt", p * 10 + c * 50)) * (param("service_tier") == "priority" ? 2 : 1) * (param("model") == "gpt-6-astra" ? 1.6 : 1)`
	params := TokenParams{P: 1000, C: 100, Len: 1000}
	for _, sample := range []struct {
		name    string
		body    string
		matches []bool
		cost    float64
	}{
		{"model only", `{"model":"gpt-6-astra"}`, []bool{false, true}, 24000},
		{"both", `{"model":"gpt-6-astra","service_tier":"priority"}`, []bool{true, true}, 48000},
		{"priority only", `{"model":"gpt-5.6-terra","service_tier":"priority"}`, []bool{true, false}, 30000},
		{"missing parameters", `{}`, []bool{false, false}, 15000},
	} {
		test.Run(sample.name, func(test *testing.T) {
			cost, trace, err := RunExprWithRequest(expression, params, RequestInput{Body: []byte(sample.body), ChannelID: 42})
			require.NoError(test, err)
			assert.Equal(test, sample.cost, cost)
			assert.Equal(test, "gpt", trace.MatchedTier)
			assert.Equal(test, []RequestRuleMatch{
				{Expression: `param("service_tier") == "priority" ? 2 : 1`, Matched: sample.matches[0]},
				{Expression: `param("model") == "gpt-6-astra" ? 1.6 : 1`, Matched: sample.matches[1]},
			}, trace.RequestRuleMatches)
		})
	}
}

func TestRequestRuleTraceUsesTheSameEvaluationAndLeavesUntakenBranchesUnknown(test *testing.T) {
	expression := `tier("base", p * 5) * (param("count") != nil && param("count") > 10 ? 1 : 1) * (has(header("X-Mode"), "fast") && hour("UTC") >= 0 ? 2 : 1)`
	plainProgram, err := expr.Compile(expression, expr.Env(getCompileEnv(1)), expr.AsFloat64())
	require.NoError(test, err)
	params := TokenParams{P: 100}
	request := RequestInput{Headers: map[string]string{"X-Mode": "fast"}, Body: []byte(`{"count":20}`)}
	plainCost, _, err := runProgram(plainProgram, params, request)
	require.NoError(test, err)
	cost, trace, err := RunExprWithRequest("v1:"+expression, params, request)
	require.NoError(test, err)
	assert.Equal(test, plainCost, cost)
	require.Len(test, trace.RequestRuleMatches, 2)
	assert.True(test, trace.RequestRuleMatches[0].Matched)
	assert.True(test, trace.RequestRuleMatches[1].Matched)

	_, trace, err = RunExprWithRequest(`channel_id == 42 ? tier("first", p) : tier("second", p) * (param("model") == "gpt-6-astra" ? 2 : 1)`, params, RequestInput{ChannelID: 42})
	require.NoError(test, err)
	assert.Empty(test, trace.RequestRuleMatches)
}
