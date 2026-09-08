package operation_setting

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type WindowExpansionPolicy struct {
	Enabled    bool    `json:"enabled"`
	ExtraLimit int     `json:"extra_limit"`
	Multiplier float64 `json:"multiplier"`
	ChannelIDs []int   `json:"channel_ids"`
}

var windowExpansionSetting = struct {
	Policy string `json:"policy"`
}{Policy: `{"enabled":false,"extra_limit":5,"multiplier":1.5,"channel_ids":[]}`}

func init() { config.GlobalConfig.Register("window_expansion_setting", &windowExpansionSetting) }

func (policy WindowExpansionPolicy) Validate() error {
	if policy.ExtraLimit < 1 || policy.ExtraLimit > 100 || policy.Multiplier <= 1 || policy.Multiplier > 10 || math.IsNaN(policy.Multiplier) || math.IsInf(policy.Multiplier, 0) || len(policy.ChannelIDs) > 16 || (policy.Enabled && len(policy.ChannelIDs) == 0) {
		return errors.New("expansion requires 1-100 additional windows, a multiplier above 1 and at most 10, and 1-16 signed Codex2API channels")
	}
	seen := make(map[int]bool)
	for _, channelID := range policy.ChannelIDs {
		if channelID <= 0 || seen[channelID] {
			return errors.New("invalid or duplicate channel id")
		}
		seen[channelID] = true
	}
	return nil
}

func GetWindowExpansionPolicy() WindowExpansionPolicy {
	var policy WindowExpansionPolicy
	if common.UnmarshalJsonStr(windowExpansionSetting.Policy, &policy) != nil || policy.Validate() != nil {
		return WindowExpansionPolicy{ExtraLimit: 5, Multiplier: 1.5, ChannelIDs: []int{}}
	}
	if policy.ChannelIDs == nil {
		policy.ChannelIDs = []int{}
	}
	return policy
}
