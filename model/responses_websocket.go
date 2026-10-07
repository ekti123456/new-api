package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"strings"
)

func (channel *Channel) SupportsResponsesWebSocket(modelName string) bool {
	if channel == nil || channel.Status != common.ChannelStatusEnabled || !channel.GetSetting().ResponsesWebSocketEnabled {
		return false
	}
	switch channel.Type {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeNewAPI, constant.ChannelTypeSub2API:
		return true
	case constant.ChannelTypeAdvancedCustom:
		route, ok := channel.GetOtherSettings().AdvancedCustom.MatchPathForModel("/v1/responses", modelName)
		return ok && (strings.TrimSpace(route.Converter) == "" || strings.TrimSpace(route.Converter) == "none")
	}
	return false
}
