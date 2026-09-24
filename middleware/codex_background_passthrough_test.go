package middleware

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestSeverBackgroundRoutingDoesNotRequireParent(test *testing.T) {
	for index, scenario := range []string{"missing", "ambiguous", "explicit missing parent", "missing identity", "existing binding"} {
		test.Run(scenario, func(test *testing.T) {
			channel, key, _ := setupCodexRootDistributorTest(test)
			// An unset switch is the deployed sever default.
			test.Setenv("CODEX_BACKGROUND_ROOT_ROUTING_ENABLED", "")
			userID, tokenID := 999501+index, 999511
			const backgroundID = "01a08502-0000-7000-8000-000000000501"
			const turnID = "01a08502-0000-7000-8000-000000000502"
			const parentID = "01a08502-0000-7000-8000-000000000503"
			background, recorder := independentCodexBackgroundContext(userID, tokenID, backgroundID, turnID)
			if scenario == "ambiguous" || scenario == "existing binding" {
				roots := []string{parentID}
				if scenario == "ambiguous" {
					roots = append(roots, "01a08502-0000-7000-8000-000000000504")
				}
				for _, root := range roots {
					main, mainRecorder := codexMainRootContext(userID, tokenID, channel.Id, root)
					main.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
					Distribute()(main)
					require.False(test, main.IsAborted(), mainRecorder.Body.String())
					_, found, err := service.LoadCodexRootChannelBinding(userID, root)
					require.NoError(test, err)
					require.True(test, found, "main requests still establish their own binding")
				}
			}
			if scenario == "explicit missing parent" || scenario == "existing binding" {
				background, recorder = codexLinkedNamingContext(userID, tokenID, parentID, backgroundID, "guardian_review")
				background.Request.Body = io.NopCloser(strings.NewReader(`{"model":"gpt-5.6-sol","input":"review"}`))
			}
			if scenario == "missing identity" {
				background.Request.Header.Del("Session-Id")
				background.Request.Header.Del("Thread-Id")
				background.Request.Header.Del("X-Client-Request-Id")
				background.Request.Header.Del("X-Codex-Window-Id")
				background.Request.Header.Set("X-Codex-Turn-Metadata", `{"thread_source":"agent_created_thread","request_kind":"turn"}`)
			}
			waitForRecentCodexRootChannelUpdate = func(context.Context, int, int, bool, time.Duration) error {
				test.Error("background routing must not wait for a parent")
				return context.DeadlineExceeded
			}
			waitForCodexRootChannelBindingUpdate = func(context.Context, int, string, time.Duration) error {
				test.Error("background routing must not wait for a root channel")
				return context.DeadlineExceeded
			}
			waitForCodexThreadRootBindingUpdate = waitForCodexRootChannelBindingUpdate
			waitForCodexTurnRootBindingUpdate = waitForCodexRootChannelBindingUpdate
			headers := background.Request.Header.Clone()
			payload, err := io.ReadAll(background.Request.Body)
			require.NoError(test, err)
			background.Request.Body = io.NopCloser(strings.NewReader(string(payload)))
			Distribute()(background)
			require.False(test, background.IsAborted(), recorder.Body.String())
			require.Equal(test, http.StatusOK, recorder.Code)
			require.Equal(test, channel.Id, common.GetContextKeyInt(background, constant.ContextKeyChannelId))
			require.Equal(test, key, common.GetContextKeyString(background, constant.ContextKeyChannelKey))
			require.False(test, common.GetContextKeyBool(background, constant.ContextKeyCodexRootChannelPinned))
			require.Empty(test, relaychannel.CodexPassiveRootSessionOverrideFeature(background))
			require.Equal(test, headers, background.Request.Header)
			forwarded, err := io.ReadAll(background.Request.Body)
			require.NoError(test, err)
			require.Equal(test, payload, forwarded)
			_, found, err := service.LoadCodexRootChannelBinding(userID, backgroundID)
			require.NoError(test, err)
			require.False(test, found, "background fallback must not create a main root")
			_, _, found, err = service.ResolveCodexThreadRootBinding(test.Context(), userID, backgroundID)
			require.NoError(test, err)
			require.False(test, found, "background routing must not invent a parent association")
		})
	}
}

func TestSeverBackgroundRoutingRetainsTokenModelAccess(test *testing.T) {
	setupCodexRootDistributorTest(test)
	test.Setenv("CODEX_BACKGROUND_ROOT_ROUTING_ENABLED", "false")
	request, recorder := independentCodexBackgroundContext(999601, 999611, "01a08502-0000-7000-8000-000000000601", "01a08502-0000-7000-8000-000000000602")
	common.SetContextKey(request, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(request, constant.ContextKeyTokenModelLimit, map[string]bool{"other-model": true})
	Distribute()(request)
	require.True(test, request.IsAborted())
	require.Equal(test, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.NotContains(test, recorder.Body.String(), "codex_background_root_unavailable")
}
