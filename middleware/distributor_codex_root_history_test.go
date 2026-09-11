package middleware

import (
	"context"
	"fmt"
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

func TestDistributorBoundRootSurvivesPrefixPublicationFailure(test *testing.T) {
	for _, failure := range []string{"binding_conflict", "prefix_overflow"} {
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
			if failure == "binding_conflict" {
				stale := binding
				stale.KeyFingerprint = "stale-key-fingerprint"
				require.NoError(test, service.StoreCodexPrefixRootCandidate(test.Context(), scope, rootID, rootID, stale))
			} else {
				for index := 0; index < 65; index++ {
					err := service.StoreCodexPrefixRootCandidate(test.Context(), scope, rootID, fmt.Sprintf("other-root-%d", index), binding)
					if index == 64 {
						require.ErrorIs(test, err, service.ErrCodexPrefixRootAmbiguous)
					} else {
						require.NoError(test, err)
					}
				}
			}

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
