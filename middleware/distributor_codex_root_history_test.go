package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDistributorBoundRootSurvivesHistoryPublicationFailure(test *testing.T) {
	for _, failure := range []string{"observation_collision", "arrival_unavailable"} {
		test.Run(failure, func(test *testing.T) {
			channel, _, fingerprint := setupCodexRootDistributorTest(test)
			useCodexRecentRootRedisFixture(test, time.Now().UTC())
			const userID, tokenID = 63001, 83001
			rootID, turnID := uuid.NewString(), uuid.NewString()
			requestContext, recorder := codexMainRootTurnContext(userID, tokenID, channel.Id, rootID, turnID)
			binding := service.CodexRootChannelBinding{
				ChannelID: channel.Id, SelectedGroup: "pro", KeyFingerprint: fingerprint,
			}
			require.NoError(test, service.StoreCodexRootChannelBinding(userID, rootID, binding))
			scope := codexPassiveRootScope(requestContext)
			arrival, err := service.BeginCodexRequestArrival(context.Background(), userID, tokenID, scope)
			require.NoError(test, err)
			require.NoError(test, service.StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrival, scope))
			state := codexRequestArrivalState{arrival: arrival}
			if failure == "observation_collision" {
				state.arrival.ArrivedAt = arrival.ArrivedAt.Add(time.Millisecond)
			} else {
				state.err = errors.New("request history temporarily unavailable")
			}
			requestContext.Set(codexRequestArrivalContextKey, state)

			Distribute()(requestContext)

			assert.False(test, requestContext.IsAborted(), recorder.Body.String())
			assert.Equal(test, http.StatusOK, recorder.Code)
			assert.Equal(test, channel.Id, common.GetContextKeyInt(requestContext, constant.ContextKeyChannelId))
			stored, found, err := service.LoadCodexRootChannelBinding(userID, rootID)
			require.NoError(test, err)
			require.True(test, found)
			assert.Equal(test, binding, stored)
			turn, found, err := service.LoadCodexTurnRootBindingContext(context.Background(), userID, turnID)
			require.NoError(test, err)
			require.True(test, found)
			assert.Equal(test, rootID, turn.RootID)
		})
	}
}
