package controller

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexDispatchRetryRequiresCurrentTrustedDiagnostic(test *testing.T) {
	for _, scenario := range []struct {
		name      string
		requestID string
		channelID int
		status    int
		advice    string
		cleared   bool
		retry     bool
	}{
		{"stop", "current", 7, 503, "stop", false, false},
		{"temporary_preserves_route", "current", 7, 503, "backoff_same_route", false, false},
		{"incomplete", "current", 7, 503, "default", false, true},
		{"previous_request", "previous", 7, 503, "stop", false, true},
		{"previous_channel", "current", 8, 503, "stop", false, true},
		{"different_failure", "current", 7, 502, "stop", false, true},
		{"new_attempt", "current", 7, 503, "stop", true, true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(common.RequestIdKey, "current")
			ctx.Set("channel_id", 7)
			attempt := common.CodexDispatchAttemptForContext(ctx)
			if scenario.cleared {
				common.ClearCodexDispatchDiagnostic(ctx)
			}
			attempt.Record(common.CodexDispatchDiagnostic{RequestID: scenario.requestID, ChannelID: scenario.channelID, Status: scenario.status, Retry: scenario.advice})
			apiError := types.NewErrorWithStatusCode(errors.New("temporarily unavailable"), types.ErrorCodeBadResponseStatusCode, 503)
			assert.Equal(test, scenario.retry, shouldRetry(ctx, apiError, 2))
		})
	}
}

func TestCodexSessionPolicyErrorsNeverRetry(test *testing.T) {
	previousRanges := operation_setting.AutomaticRetryStatusCodeRanges
	operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 400, End: 599}}
	test.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = previousRanges })
	for _, code := range []types.ErrorCode{"codex_root_account_wait_timeout", "codex_background_root_unavailable", "session_model_unavailable"} {
		requestContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		requestError := types.NewErrorWithStatusCode(errors.New("background root unavailable"), code, 400)
		require.False(test, shouldRetry(requestContext, requestError, 5), string(code))
	}
}
