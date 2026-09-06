package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicPricingHidesChannelRulesWithoutChangingBillingConfig(test *testing.T) {
	expression := `(len > 272000 && channel_id == 42 ? tier("gpt", p * 10 + c * 45 + cr * 1 + cc * 12.5) : tier("272k", p * 5 + c * 30 + cr * 0.5 + cc * 6.25)) * (param("service_tier") == "priority" ? 2 : 1)`
	pricing := []model.Pricing{{ModelName: "test", BillingMode: "tiered_expr", BillingExpr: expression}}
	public := preparePublicPricing(pricing)
	require.Len(test, public, 1)
	assert.True(test, public[0].HideTieredPricing)
	assert.Equal(test, `tier("base", p * 5 + c * 30 + cr * 0.5 + cc * 6.25) * (param("service_tier") == "priority" ? 2 : 1)`, public[0].BillingExpr)
	assert.Equal(test, expression, pricing[0].BillingExpr)
	assert.False(test, pricing[0].HideTieredPricing)
}
