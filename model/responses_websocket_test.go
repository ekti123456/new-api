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
			seedChannelRoutingModeTestChannel(t, 9304, 1, false)
			peer, err := GetChannelById(9304, true)
			require.NoError(t, err)
			peer.Type = constant.ChannelTypeOpenAI
			peer.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: true})
			require.NoError(t, DB.Save(peer).Error)
			InitChannelCache()
			options := ChannelRoutingOptions{ResponsesWebSocket: true, FullWSChannels: map[int]bool{9302: true}}
			selected, err := options.SelectChannel("default", "gpt-5", 0, "/v1/responses")
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.Equal(t, peer.Id, selected.Id, "an equal-priority peer can take a new connection")
			options.FullWSChannels[peer.Id] = true
			selected, err = options.SelectChannel("default", "gpt-5", 0, "/v1/responses")
			require.NoError(t, err)
			require.Nil(t, selected, "HTTP-only and UA-reserved channels remain ineligible")
		})
	}
}
