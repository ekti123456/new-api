package service

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func applyWindowExpansionQuota(info *relaycommon.RelayInfo, quota int) int {
	if info.WindowMultiplier() == 1 {
		return quota
	}
	result, clamp := common.QuotaRoundChecked(float64(quota) * info.WindowMultiplier())
	noteQuotaClamp(info, clamp)
	return result
}
