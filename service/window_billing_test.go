package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWindowBillingOrdinaryExpandedAndOverflow(test *testing.T) {
	info := &relaycommon.RelayInfo{}
	require.Equal(test, 101, applyWindowExpansionQuota(info, 101))
	info.WindowBilling = &relaycommon.WindowBillingGrant{Expanded: true, Multiplier: 1.5}
	require.Equal(test, 152, applyWindowExpansionQuota(info, 101))
	require.Zero(test, applyWindowExpansionQuota(info, 0))
	require.Equal(test, common.MaxQuota, applyWindowExpansionQuota(info, common.MaxQuota))
	require.NotNil(test, info.QuotaClamp)
}

func TestWindowBillingTieredRouteAndFallbackDoNotLoseOrDoubleSurcharge(test *testing.T) {
	info := &relaycommon.RelayInfo{
		WindowBilling:         &relaycommon.WindowBillingGrant{Expanded: true, Multiplier: 1.5},
		PriceData:             types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 2}},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", GroupRatio: 1, EstimatedQuotaBeforeGroup: 100, EstimatedQuotaAfterGroup: 150},
	}
	snapshot, err := refreshTieredBillingRoute(info, 1)
	require.NoError(test, err)
	require.Equal(test, 300, snapshot.EstimatedQuotaAfterGroup)
	info.FinalPreConsumedQuota = 300
	used, fallback, result := TryTieredSettle(info, billingexpr.TokenParams{P: 100})
	require.True(test, used)
	require.Nil(test, result)
	require.Equal(test, 300, fallback)
	summary := textQuotaSummary{ToolCallSurchargeQuota: decimal.NewFromInt(20)}
	require.Equal(test, 330, composeTieredTextQuota(info, summary, fallback, result))
}
