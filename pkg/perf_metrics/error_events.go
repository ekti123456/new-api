package perfmetrics

import (
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

func truncatePerfMetricErrorText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) <= maxPerfMetricErrorReasonBytes {
		return value
	}
	value = value[:maxPerfMetricErrorReasonBytes]
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
	if info == nil || err == nil || info.ExcludeFromPerformanceMetrics || !perf_metrics_setting.GetSetting().Enabled {
		return
	}

	item := &model.PerfMetricError{
		CreatedAt:   time.Now().Unix(),
		UserId:      info.UserId,
		ModelName:   info.OriginModelName,
		Group:       info.UsingGroup,
		ChannelId:   info.ChannelId,
		ErrorType:   string(err.GetErrorType()),
		ErrorCode:   string(err.GetErrorCode()),
		StatusCode:  err.StatusCode,
		ErrorReason: truncatePerfMetricErrorText(err.MaskSensitiveErrorWithStatusCode()),
	}
	if diagnostic, ok := common.GetCodexDispatchDiagnostic(c, info.ChannelId, err.StatusCode); ok {
		if payload, marshalErr := common.Marshal(diagnostic); marshalErr == nil {
			item.ErrorCode = "codex_dispatch_" + diagnostic.Reason
			item.ErrorReason = truncatePerfMetricErrorText(string(payload))
		}
	}
	if item.Group == "" {
		item.Group = info.TokenGroup
	}
	if c != nil {
		item.Username = truncatePerfMetricErrorText(c.GetString("username"))
		item.RequestId = truncatePerfMetricErrorText(c.GetString(common.RequestIdKey))
		item.UpstreamRequestId = truncatePerfMetricErrorText(c.GetString(common.UpstreamRequestIdKey))
		item.ChannelName = truncatePerfMetricErrorText(c.GetString("channel_name"))
		if c.Request != nil {
			if c.Request.URL != nil {
				item.RequestPath = truncatePerfMetricErrorText(c.Request.URL.Path)
			}
			item.UserAgent = truncatePerfMetricErrorText(c.Request.UserAgent())
		}
	}
	item.TokenId = info.TokenId
	if err := model.CreatePerfMetricError(item); err != nil {
		common.SysError("failed to record performance error: " + err.Error())
	}
}
