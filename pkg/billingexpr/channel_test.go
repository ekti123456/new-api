package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelConditionUsesTrustedSelectedChannel(test *testing.T) {
	expression := `channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 45) : tier("base", p * 5 + c * 30)`
	for _, sample := range []struct {
		channelID int
		length    float64
		tier      string
	}{
		{12, 272000, "base"},
		{12, 272001, "long"},
		{15, 400000, "base"},
		{0, 400000, "base"},
	} {
		request := RequestInput{ChannelID: sample.channelID, Headers: map[string]string{"channel_id": "12"}, Body: []byte(`{"channel_id":12}`)}
		_, trace, err := RunExprWithRequest(expression, TokenParams{P: sample.length, Len: sample.length, C: 1}, request)
		require.NoError(test, err)
		assert.Equal(test, sample.tier, trace.MatchedTier)
	}
}

func TestPublicPricingHidesChannelTiersAndPreservesOrdinaryTiers(test *testing.T) {
	for _, sample := range []struct {
		expression string
		public     string
		hidden     bool
	}{
		{`channel_id == 12 && len > 272000 ? tier("secret", p * 10 + c * 45) : tier("base", p * 5 + c * 30)`, `tier("base", p * 5 + c * 30)`, true},
		{`v1:channel_id != 12 ? tier("secret", p * 10) : len > 272000 ? tier("long", p * 8) : tier("normal", p * 5 + cr * 0.5)`, `tier("base", p * 5 + cr * 0.5)`, true},
		{`tier("custom", p * channel_id)`, "", true},
		{`len > 272000 ? tier("long", p * 10) : tier("base", p * 5)`, `len > 272000 ? tier("long", p * 10) : tier("base", p * 5)`, false},
		{`tier("channel_id", p * 5)`, `tier("channel_id", p * 5)`, false},
	} {
		public, hidden := PublicPricingExpr(sample.expression)
		assert.Equal(test, sample.public, public, sample.expression)
		assert.Equal(test, sample.hidden, hidden, sample.expression)
	}
}

func TestPublicPricingPreservesRequestMultipliers(test *testing.T) {
	pricing := `len > 272000 && channel_id == 42 ? tier("gpt", p * 10 + c * 45 + cr * 1 + cc * 12.5) : tier("272k", p * 5 + c * 30 + cr * 0.5 + cc * 6.25)`
	base := `tier("base", p * 5 + c * 30 + cr * 0.5 + cc * 6.25)`
	priority := `param("service_tier") == "priority" ? 2 : 1`
	for _, sample := range []struct {
		name       string
		expression string
		public     string
	}{
		{"priority", "(" + pricing + ") * (" + priority + ")", base + " * (" + priority + ")"},
		{"multiple rules", "v1:(" + pricing + ") * (" + priority + `) * (header("x-fast") == "on" ? 3 : 1)`, base + " * (" + priority + `) * (header("x-fast") == "on" ? 3 : 1)`},
		{"reversed factors", "(" + priority + ") * (" + pricing + ")", "(" + priority + ") * " + base},
		{"private multiplier", "(" + pricing + `) * (channel_id == 42 ? 2 : 1)`, ""},
	} {
		test.Run(sample.name, func(test *testing.T) {
			public, hidden := PublicPricingExpr(sample.expression)
			assert.True(test, hidden)
			assert.Equal(test, sample.public, public)
			assert.NotContains(test, public, "channel_id")
			if public == "" {
				return
			}
			for _, body := range []string{`{}`, `{"service_tier":"priority"}`} {
				request := RequestInput{ChannelID: 22, Body: []byte(body)}
				params := TokenParams{P: 300000, Len: 300000, C: 100, CR: 20000, CC: 10000}
				actual, _, err := RunExprWithRequest(sample.expression, params, request)
				require.NoError(test, err)
				displayed, _, err := RunExprWithRequest(public, params, request)
				require.NoError(test, err)
				assert.Equal(test, actual, displayed)
			}
		})
	}
}
