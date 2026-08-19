package setting

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var ModelRequestRateLimitEnabled = false
var ModelRequestRateLimitDurationMinutes = 1
var ModelRequestRateLimitCount = 0
var ModelRequestRateLimitSuccessCount = 1000
var ModelRequestRateLimitGroup = map[string][2]int{}
var ModelRequestRateLimitMutex sync.RWMutex

// ModelRPMRateLimitEnabled enables independent per-user RPM limits for only
// the models listed in ModelRPMRateLimitModels. Unlisted models are unaffected.
var ModelRPMRateLimitEnabled = false
var ModelRPMRateLimitModels = map[string]int{}
var ModelRPMRateLimitModelsMutex sync.RWMutex

// ModelRequestConcurrencyLimitEnabled controls per-user active request limits.
// The default is enabled so new installations protect long-lived streaming
// requests even when the time-window request limiter is disabled.
var ModelRequestConcurrencyLimitEnabled = true

// DefaultUserConcurrencyLimit is used when a user has no explicit override.
var DefaultUserConcurrencyLimit = 5

// UserConcurrencyCooldownSeconds keeps a completed request's slot occupied for
// a short cooldown period. The response is not delayed.
var UserConcurrencyCooldownSeconds = 3

func ModelRequestRateLimitGroup2JSONString() string {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	jsonBytes, err := json.Marshal(ModelRequestRateLimitGroup)
	if err != nil {
		common.SysLog("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitGroupByJSONString(jsonStr string) error {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	ModelRequestRateLimitGroup = make(map[string][2]int)
	return json.Unmarshal([]byte(jsonStr), &ModelRequestRateLimitGroup)
}

func GetGroupRateLimit(group string) (totalCount, successCount int, found bool) {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	if ModelRequestRateLimitGroup == nil {
		return 0, 0, false
	}

	limits, found := ModelRequestRateLimitGroup[group]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

func ModelRPMRateLimitModels2JSONString() string {
	ModelRPMRateLimitModelsMutex.RLock()
	defer ModelRPMRateLimitModelsMutex.RUnlock()

	jsonBytes, err := common.Marshal(ModelRPMRateLimitModels)
	if err != nil {
		common.SysLog("error marshalling model RPM limits: " + err.Error())
		return "{}"
	}
	return string(jsonBytes)
}

func UpdateModelRPMRateLimitModelsByJSONString(jsonStr string) error {
	parsed := make(map[string]int)
	if err := common.UnmarshalJsonStr(jsonStr, &parsed); err != nil {
		return err
	}
	if err := CheckModelRPMRateLimitModels(jsonStr); err != nil {
		return err
	}
	ModelRPMRateLimitModelsMutex.Lock()
	ModelRPMRateLimitModels = parsed
	ModelRPMRateLimitModelsMutex.Unlock()
	return nil
}

func GetModelRPMLimit(modelName string) (int, bool) {
	ModelRPMRateLimitModelsMutex.RLock()
	defer ModelRPMRateLimitModelsMutex.RUnlock()

	limit, found := ModelRPMRateLimitModels[modelName]
	if found {
		return limit, true
	}
	if strings.HasSuffix(modelName, ratio_setting.CompactModelSuffix) {
		limit, found = ModelRPMRateLimitModels[strings.TrimSuffix(modelName, ratio_setting.CompactModelSuffix)]
	}
	return limit, found
}

func CheckModelRPMRateLimitModels(jsonStr string) error {
	parsed := make(map[string]int)
	if err := common.UnmarshalJsonStr(jsonStr, &parsed); err != nil {
		return err
	}
	for modelName, rpm := range parsed {
		if strings.TrimSpace(modelName) == "" {
			return fmt.Errorf("model name cannot be empty")
		}
		if rpm < 1 || rpm > math.MaxInt32 {
			return fmt.Errorf("model %s RPM must be between 1 and %d", modelName, math.MaxInt32)
		}
	}
	return nil
}

func CheckModelRequestRateLimitGroup(jsonStr string) error {
	checkModelRequestRateLimitGroup := make(map[string][2]int)
	err := json.Unmarshal([]byte(jsonStr), &checkModelRequestRateLimitGroup)
	if err != nil {
		return err
	}
	for group, limits := range checkModelRequestRateLimitGroup {
		if limits[0] < 0 || limits[1] < 1 {
			return fmt.Errorf("group %s has negative rate limit values: [%d, %d]", group, limits[0], limits[1])
		}
		if limits[0] > math.MaxInt32 || limits[1] > math.MaxInt32 {
			return fmt.Errorf("group %s [%d, %d] has max rate limits value 2147483647", group, limits[0], limits[1])
		}
	}

	return nil
}
