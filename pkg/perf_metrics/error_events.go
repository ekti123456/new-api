package perfmetrics

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
)

const maxPerfMetricErrorReasonBytes = 8192

func cleanupPerfMetricErrorsLoop() {
	for {
		if err := model.DeleteExpiredPerfMetricErrors(time.Now()); err != nil {
			common.SysError("failed to cleanup expired performance errors: " + err.Error())
		}
		time.Sleep(5 * time.Minute)
	}
}

func truncatePerfMetricErrorText(value string, maxBytes int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// RecordRelayError persists the final error that is also counted as a failed
// performance sample. It intentionally does not depend on ERROR_LOG_ENABLED,
// so administrators can explain performance failures even when user-facing
// error logs are disabled.
func RecordRelayError(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError) {
	if info == nil || !perf_metrics_setting.GetSetting().Enabled {
		return
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if ClassifyRelayOutcome(ctx, info, err) != OutcomeFailure {
		return
	}
	if err == nil {
		stream := info.StreamStatus.OutcomeSnapshot()
		status := stream.ErrorStatus
		if status < 400 || status > 599 {
			status = 502
		}
		code := stream.ErrorCode
		if code == "" {
			code = "upstream_stream_failed"
		}
		err = types.NewOpenAIError(errors.New("Upstream stream failed before successful completion"), types.ErrorCode(code), status)
	}
	if model.IsSessionWindowCapacityError(err.StatusCode, string(err.GetErrorCode())) {
		return
	}

	item := &model.PerfMetricError{
		CreatedAt:   time.Now().Unix(),
		UserId:      info.UserId,
		ModelName:   truncatePerfMetricErrorText(info.OriginModelName, 128),
		Group:       truncatePerfMetricErrorText(info.UsingGroup, 64),
		ChannelId:   info.ChannelId,
		ErrorType:   truncatePerfMetricErrorText(string(err.GetErrorType()), 128),
		ErrorCode:   truncatePerfMetricErrorText(string(err.GetErrorCode()), 128),
		StatusCode:  err.StatusCode,
		ErrorReason: truncatePerfMetricErrorText(err.MaskSensitiveErrorWithStatusCode(), maxPerfMetricErrorReasonBytes),
	}
	if diagnostic, ok := common.GetCodexDispatchDiagnostic(c, info.ChannelId, err.StatusCode); ok {
		if payload, marshalErr := common.Marshal(diagnostic); marshalErr == nil {
			item.ErrorCode = "codex_dispatch_" + diagnostic.Reason
			item.ErrorReason = truncatePerfMetricErrorText(string(payload), maxPerfMetricErrorReasonBytes)
		}
	}
	if item.Group == "" {
		item.Group = truncatePerfMetricErrorText(info.TokenGroup, 64)
	}
	if c != nil {
		item.Username = truncatePerfMetricErrorText(c.GetString("username"), 128)
		item.RequestId = truncatePerfMetricErrorText(c.GetString(common.RequestIdKey), 128)
		item.UpstreamRequestId = truncatePerfMetricErrorText(c.GetString(common.UpstreamRequestIdKey), 128)
		item.ChannelName = truncatePerfMetricErrorText(c.GetString("channel_name"), 128)
		if c.Request != nil {
			if c.Request.URL != nil {
				item.RequestPath = truncatePerfMetricErrorText(c.Request.URL.Path, 256)
			}
			item.UserAgent = truncatePerfMetricErrorText(c.Request.UserAgent(), 512)
		}
	}
	item.TokenId = info.TokenId
	if err := model.CreatePerfMetricError(item); err != nil {
		common.SysError("failed to record performance error: " + err.Error())
	}
}
