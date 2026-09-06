package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func codexAmbientSuggestionContext(userID, tokenID int) (*gin.Context, *httptest.ResponseRecorder) {
	const threadID = "01a075e6-d1d1-7170-a523-076c03592fda"
	requestContext, recorder := codexUnlinkedTitleContext(userID, tokenID, threadID)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-terra","input":"Generate suggestions for this project"}`))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	requestContext.Request.Header.Set("Session-Id", threadID)
	requestContext.Request.Header.Set("X-Codex-Installation-Id", "ambient-test-device")
	requestContext.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+threadID+`","thread_id":"`+threadID+`","turn_id":"01a075e6-d2a2-7a61-8047-f3611e0acd17","thread_source":"ambient_suggestions","request_kind":"turn"}`)
	return requestContext, recorder
}

func TestAmbientSuggestionsWaitForRootAndPinSubsequentSteps(test *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		test.Run(backend, func(test *testing.T) {
			channel, key, fingerprint := setupCodexRootDistributorTest(test)
			if backend == "redis" {
				useCodexRecentRootRedisFixture(test, time.Now())
			}
			const userID, tokenID = 98241, 98242
			const rootID = "01a075e7-3ed6-7a21-b4d8-7018562f6648"
			requestContext, recorder := codexAmbientSuggestionContext(userID, tokenID)
			scope := codexPassiveRootScope(requestContext)
			binding := service.CodexRootChannelBinding{ChannelID: channel.Id, SelectedGroup: "pro", KeyFingerprint: fingerprint}
			waitCalls := 0
			waitForRecentCodexTitleRootUpdate = func(waitContext context.Context, gotUserID, gotTokenID int, remaining time.Duration) error {
				waitCalls++
				require.Equal(test, 1, waitCalls, "dispatch must not wait again after a unique root appears")
				require.NoError(test, waitContext.Err())
				require.Equal(test, userID, gotUserID)
				require.Equal(test, tokenID, gotTokenID)
				require.Positive(test, remaining)
				require.NoError(test, service.StoreProvisionalCodexRootChannelBinding(userID, rootID, binding))
				require.NoError(test, service.StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding, scope))
				require.NoError(test, service.StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding, scope))
				return nil
			}
			Distribute()(requestContext)
			require.Equal(test, 1, waitCalls)
			require.False(test, requestContext.IsAborted(), recorder.Body.String())
			require.Equal(test, key, common.GetContextKeyString(requestContext, constant.ContextKeyChannelKey))
			require.Equal(test, rootID, relaychannel.ResolveCodexRootSessionForDistribution(requestContext).RootID)

			const competingRootID = "01a075e8-3ed6-7a21-b4d8-7018562f6648"
			require.NoError(test, service.StoreCodexRootChannelBinding(userID, competingRootID, binding))
			require.NoError(test, service.StoreRecentCodexRootChannelCandidate(userID, tokenID, competingRootID, binding, scope))
			require.NoError(test, service.StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, competingRootID, binding, scope))
			for _, nextTurn := range []string{"01a075e6-d2a2-7a61-8047-f3611e0acd17", "01a075e6-d2a2-7a61-8047-f3611e0acd18"} {
				nextContext, nextRecorder := codexAmbientSuggestionContext(userID, tokenID)
				nextContext.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(nextContext.Request.Header.Get("X-Codex-Turn-Metadata"), "01a075e6-d2a2-7a61-8047-f3611e0acd17", nextTurn))
				Distribute()(nextContext)
				require.False(test, nextContext.IsAborted(), nextRecorder.Body.String())
				require.Equal(test, rootID, relaychannel.ResolveCodexRootSessionForDistribution(nextContext).RootID)
				require.True(test, common.GetContextKeyBool(nextContext, constant.ContextKeyCodexRootChannelPinned))
			}
			require.Equal(test, 1, waitCalls)
		})
	}
}

func TestAmbientSuggestionsRequireUniqueAuthorizedRoot(test *testing.T) {
	for testIndex, scenario := range []string{"ready", "ua root", "timeout", "missing identity", "cancelled", "ambiguous", "other device", "other token", "other group"} {
		test.Run(scenario, func(test *testing.T) {
			channel, _, fingerprint := setupCodexRootDistributorTest(test)
			userID, tokenID := 98300+testIndex, 98400+testIndex
			requestContext, recorder := codexAmbientSuggestionContext(userID, tokenID)
			scope := codexPassiveRootScope(requestContext)
			binding := service.CodexRootChannelBinding{ChannelID: channel.Id, SelectedGroup: "pro", KeyFingerprint: fingerprint}
			if scenario == "ua root" {
				require.NoError(test, model.DB.Model(channel).Update("ua_routing_only", true).Error)
				model.InitChannelCache()
				setting := operation_setting.GetUserAgentRoutingSetting()
				originalSetting := *setting
				test.Cleanup(func() { *setting = originalSetting })
				setting.Enabled = true
				setting.UserAgentWhitelist = []string{"codex-tui"}
				setting.ChannelIDs = []int{channel.Id}
				setting.GroupNames = []string{"pro"}
				binding.UARoutingOnly = true
			}
			if scenario == "missing identity" {
				requestContext.Request.Header.Del("Session-Id")
				requestContext.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_source":"ambient_suggestions","request_kind":"turn"}`)
			} else if scenario == "cancelled" {
				cancelContext, cancel := context.WithCancel(requestContext.Request.Context())
				cancel()
				requestContext.Request = requestContext.Request.WithContext(cancelContext)
			} else if scenario != "timeout" {
				if scenario == "other device" {
					scope.InstallationID = "another-device"
				}
				if scenario == "other token" {
					scope.TokenID++
				}
				if scenario == "other group" {
					binding.SelectedGroup = "different-group"
				}
				rootIDs := []string{"01a075e7-3ed6-7a21-b4d8-7018562f6648"}
				if scenario == "ambiguous" {
					rootIDs = append(rootIDs, "01a075e8-3ed6-7a21-b4d8-7018562f6648")
				}
				for _, rootID := range rootIDs {
					require.NoError(test, service.StoreCodexRootChannelBinding(userID, rootID, binding))
					require.NoError(test, service.StoreRecentCodexRootChannelCandidate(userID, scope.TokenID, rootID, binding, scope))
					require.NoError(test, service.StoreProvisionalCodexTitleRootChannelCandidate(userID, scope.TokenID, rootID, binding, scope))
				}
			}
			Distribute()(requestContext)
			if scenario == "ready" || scenario == "ua root" {
				require.False(test, requestContext.IsAborted(), recorder.Body.String())
				require.Equal(test, channel.Id, common.GetContextKeyInt(requestContext, constant.ContextKeyChannelId))
				require.True(test, common.GetContextKeyBool(requestContext, constant.ContextKeyCodexRootChannelPinned))
				return
			}
			require.Equal(test, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.True(test, requestContext.IsAborted())
			require.Zero(test, common.GetContextKeyInt(requestContext, constant.ContextKeyChannelId))
			_, found, err := service.LoadCodexPassiveRootAlias(context.Background(), userID, tokenID, "01a075e6-d1d1-7170-a523-076c03592fda", codexPassiveRootScope(requestContext))
			require.NoError(test, err)
			require.False(test, found)
		})
	}
}
