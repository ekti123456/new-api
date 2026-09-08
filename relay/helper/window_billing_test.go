package helper

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestWindowBillingPreconsumeMatchesEachPricingModeAndRecalculation(test *testing.T) {
	saved := map[string]string{}
	require.NoError(test, config.GlobalConfig.SaveToDB(func(key, value string) error { saved[key] = value; return nil }))
	prices, ratios := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	test.Cleanup(func() {
		require.NoError(test, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(test, ratio_setting.UpdateModelPriceByJSONString(prices))
		require.NoError(test, ratio_setting.UpdateModelRatioByJSONString(ratios))
	})
	require.NoError(test, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"window-tiered":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"window-tiered":"tier(\"base\", p * 2)"}`,
	}))
	require.NoError(test, ratio_setting.UpdateModelPriceByJSONString(`{"window-fixed":0.002}`))
	require.NoError(test, ratio_setting.UpdateModelRatioByJSONString(`{"window-token":1}`))
	for _, name := range []string{"window-tiered", "window-fixed", "window-token"} {
		test.Run(name, func(test *testing.T) {
			request, _ := gin.CreateTestContext(httptest.NewRecorder())
			request.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			request.Set("group", "default")
			info := &relaycommon.RelayInfo{OriginModelName: name, UserGroup: "default", UsingGroup: "default"}
			base, err := ModelPriceHelper(request, info, 1000, &types.TokenCountMeta{})
			require.NoError(test, err)
			info.WindowBilling = &relaycommon.WindowBillingGrant{Expanded: true, Multiplier: 1.5}
			for range 2 {
				priced, err := ModelPriceHelper(request, info, 1000, &types.TokenCountMeta{})
				require.NoError(test, err)
				require.Equal(test, common.QuotaFromFloat(float64(base.QuotaToPreConsume)*1.5), priced.QuotaToPreConsume)
				if info.TieredBillingSnapshot != nil {
					require.Equal(test, priced.QuotaToPreConsume, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup)
				}
			}
		})
	}
}
