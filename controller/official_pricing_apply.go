package controller

import (
	"fmt"
	"math"
	"net/http"
	"reflect"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
)

var officialPricingSaveMutex sync.Mutex
var officialPricingOptionFields = map[string]string{
	"ModelRatio": "model_ratio", "ModelPrice": "model_price", "CompletionRatio": "completion_ratio",
	"CacheRatio": "cache_ratio", "CreateCacheRatio": "create_cache_ratio", "ImageRatio": "image_ratio",
	"AudioRatio": "audio_ratio", "AudioCompletionRatio": "audio_completion_ratio",
	"billing_setting.billing_mode": "billing_mode", "billing_setting.billing_expr": "billing_expr",
}

type officialPricingUpdate struct {
	Before map[string]string `json:"before"`
	Values map[string]string `json:"values"`
}

func validateOfficialPricingUpdate(update officialPricingUpdate, current map[string]any) error {
	if len(update.Values) == 0 {
		return fmt.Errorf("No model price changes to save")
	}
	for key, value := range update.Values {
		field, allowed := officialPricingOptionFields[key]
		if !allowed {
			return fmt.Errorf("Invalid pricing configuration")
		}
		var before, after map[string]any
		if common.UnmarshalJsonStr(update.Before[key], &before) != nil || before == nil || common.UnmarshalJsonStr(value, &after) != nil || after == nil {
			return fmt.Errorf("Invalid pricing configuration")
		}
		actual := map[string]any{}
		if data, exists := current[field]; exists {
			raw, err := common.Marshal(data)
			if err != nil {
				return err
			}
			if err := common.Unmarshal(raw, &actual); err != nil {
				return err
			}
		}
		if !reflect.DeepEqual(before, actual) {
			return fmt.Errorf("Pricing changed since preview. Fetch prices again before saving.")
		}
		for name, raw := range after {
			if name == "" {
				return fmt.Errorf("Invalid pricing configuration")
			}
			switch field {
			case "billing_mode":
				if raw != "ratio" && raw != "tiered_expr" {
					return fmt.Errorf("Invalid pricing configuration")
				}
			case "billing_expr":
				expression, ok := raw.(string)
				if !ok {
					return fmt.Errorf("Invalid pricing configuration")
				}
				if before[name] != expression && expression != "" {
					if err := billing_setting.SmokeTestExpr(expression); err != nil {
						return fmt.Errorf("invalid billing expression for %s: %w", name, err)
					}
				}
			default:
				number, ok := raw.(float64)
				if !ok || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
					return fmt.Errorf("Invalid pricing configuration")
				}
			}
		}
	}
	return nil
}

// Related prices and expression settings are persisted together, so a failed
// database write cannot leave half of a model's pricing imported.
func ApplyOfficialPrices(c *gin.Context) {
	var update officialPricingUpdate
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 20<<20), &update); err != nil {
		common.ApiErrorMsg(c, "Invalid pricing configuration")
		return
	}
	officialPricingSaveMutex.Lock()
	defer officialPricingSaveMutex.Unlock()
	// Compare against exactly the configuration served by GET /api/option,
	// including empty maps, rather than derived/default effective billing rates.
	current := make(map[string]any)
	common.OptionMapRWMutex.RLock()
	for key, field := range officialPricingOptionFields {
		value := map[string]any{}
		if raw := common.OptionMap[key]; raw != "" {
			if err := common.UnmarshalJsonStr(raw, &value); err != nil {
				common.OptionMapRWMutex.RUnlock()
				common.ApiErrorMsg(c, "Invalid pricing configuration")
				return
			}
		}
		current[field] = value
	}
	common.OptionMapRWMutex.RUnlock()
	if err := validateOfficialPricingUpdate(update, current); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(update.Values); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "pricing.official_sync", map[string]interface{}{"option_count": len(update.Values)})
	common.ApiSuccess(c, nil)
}
