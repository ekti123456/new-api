package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketSelectionPreservesHTTPAndRoutingPools(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			resetChannelRoutingModeTestTables(t, cached)
			for _, fixture := range []struct {
				id       int
				priority int64
				ua, ws   bool
			}{{9301, 99, false, false}, {9302, 1, false, true}, {9303, 100, true, true}} {
				seedChannelRoutingModeTestChannel(t, fixture.id, fixture.priority, fixture.ua)
				channel, err := GetChannelById(fixture.id, true)
				require.NoError(t, err)
				channel.Type = constant.ChannelTypeOpenAI
				channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: fixture.ws})
				require.NoError(t, DB.Save(channel).Error)
			}
			InitChannelCache()
			for _, scenario := range []struct {
				ws, ua bool
				want   int
			}{{false, false, 9301}, {true, false, 9302}, {true, true, 9303}} {
				channel, err := GetRandomSatisfiedChannelWithRoutingMode("default", "gpt-5", 0, "/v1/responses", scenario.ua, scenario.ws)
				require.NoError(t, err)
				require.NotNil(t, channel)
				require.Equal(t, scenario.want, channel.Id)
			}
		})
	}
}
