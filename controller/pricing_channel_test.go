package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicPricingHidesChannelRulesWithoutChangingBillingConfig(test *testing.T) {
	expression := `channel_id == 12 && len > 272000 ? tier("supplier", p * 10) : tier("base", p * 5 + c * 30)`
	pricing := []model.Pricing{{ModelName: "test", BillingMode: "tiered_expr", BillingExpr: expression}}
	public := preparePublicPricing(pricing)
	require.Len(test, public, 1)
	assert.True(test, public[0].HideTieredPricing)
	assert.Equal(test, `tier("base", p * 5 + c * 30)`, public[0].BillingExpr)
	assert.Equal(test, expression, pricing[0].BillingExpr)
	assert.False(test, pricing[0].HideTieredPricing)
}
