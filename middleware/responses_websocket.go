package middleware

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type ResponsesWSConnectionPin struct {
	ChannelID int
	Key       string
	KeyIndex  int
}

func responsesWSChannelPin(c *gin.Context, modelName string) (*model.Channel, string, error) {
	value, _ := c.Get("responses_websocket_pin")
	pin, _ := value.(ResponsesWSConnectionPin)
	if pin.ChannelID == 0 {
		return nil, "", nil
	}
	channel, err := model.CacheGetChannel(pin.ChannelID)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled || !channel.SupportsResponsesWebSocket(modelName) {
		return nil, "", errors.New("Responses WebSocket channel is no longer available; reconnect required")
	}
	key, keyErr := channel.GetEnabledKeyAt(pin.KeyIndex)
	if keyErr != nil || key != pin.Key {
		return nil, "", errors.New("upstream credential changed; reconnect required")
	}
	if channel.UARoutingOnly != common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted) {
		return nil, "", errors.New("the connection channel is no longer allowed by routing policy")
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	groups := []string{group}
	if group == "auto" {
		groups = service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	for _, allowed := range groups {
		if model.IsChannelEnabledForGroupModel(allowed, modelName, channel.Id) {
			if group == "auto" {
				common.SetContextKey(c, constant.ContextKeyAutoGroup, allowed)
			}
			return channel, allowed, nil
		}
	}
	return nil, "", errors.New("the connection channel is no longer allowed for this group and model")
}
