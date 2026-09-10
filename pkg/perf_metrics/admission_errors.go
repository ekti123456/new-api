package perfmetrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
)

type admissionErrorEvent struct {
	performance *model.PerfMetricError
	usage       *model.Log
	requestID   string
	flushed     chan struct{}
}

type admissionErrorWriter struct {
	once  sync.Once
	queue chan admissionErrorEvent
}

var admissionErrors = &admissionErrorWriter{queue: make(chan admissionErrorEvent, 256)}

func (writer *admissionErrorWriter) run() {
	for event := range writer.queue {
		if event.flushed != nil {
			close(event.flushed)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := model.CreateRelayAdmissionError(ctx, event.performance, event.usage)
		cancel()
		if err != nil {
			logger.LogError(nil, fmt.Sprintf("admission_error_persist_failed request_id=%s; see relay_admission_rejected runtime log: %v", event.requestID, err))
		}
	}
}

func (writer *admissionErrorWriter) enqueue(event admissionErrorEvent) bool {
	writer.once.Do(func() { go writer.run() })
	select {
	case writer.queue <- event:
		return true
	default:
		return false
	}
}

func FlushAdmissionErrors(ctx context.Context) error {
	admissionErrors.once.Do(func() { go admissionErrors.run() })
	flushed := make(chan struct{})
	select {
	case admissionErrors.queue <- admissionErrorEvent{flushed: flushed}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-flushed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func admissionErrorText(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return strings.Clone(strings.TrimSpace(strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || character == 0 {
			return -1
		}
		return character
	}, value)))
}

func RecordAdmissionError(c *gin.Context, modelName string, statusCode int, code, message string, metadata map[string]string) {
	if c == nil || c.Request == nil || c.GetInt("id") <= 0 {
		return
	}
	requestID := admissionErrorText(c.GetString(common.RequestIdKey), 64)
	if requestID == "" {
		requestID = common.NewRequestId()
		c.Set(common.RequestIdKey, requestID)
	}
	code = admissionErrorText(code, 128)
	if code == "" {
		code = "relay_admission_rejected"
	}
	requestPath := ""
	if c.Request.URL != nil {
		requestPath = admissionErrorText(c.Request.URL.Path, 256)
	}
	modelName = admissionErrorText(modelName, 128)
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "" {
		group = c.GetString("group")
	}
	group = admissionErrorText(group, 64)
	details := make(map[string]string, len(metadata))
	for field, value := range metadata {
		if value != "" {
			details[field] = admissionErrorText(value, 128)
		}
	}
	diagnostic := map[string]any{
		"stage":            "relay_admission",
		"request_id":       requestID,
		"user_id":          c.GetInt("id"),
		"model":            modelName,
		"status_code":      statusCode,
		"error_code":       code,
		"message":          admissionErrorText(message, 2048),
		"request_path":     requestPath,
		"request_method":   c.Request.Method,
		"request_metadata": details,
	}
	reason := common.GetJsonString(diagnostic)
	logger.LogError(c, "relay_admission_rejected "+reason)
	performanceEnabled := perf_metrics_setting.GetSetting().Enabled
	if !performanceEnabled && !constant.ErrorLogEnabled {
		return
	}
	now := time.Now().Unix()
	username := admissionErrorText(c.GetString("username"), 128)
	userAgent := admissionErrorText(c.Request.UserAgent(), 512)
	event := admissionErrorEvent{requestID: requestID}
	if performanceEnabled {
		event.performance = &model.PerfMetricError{
			CreatedAt: now, UserId: c.GetInt("id"), Username: username,
			ModelName: modelName, Group: group, TokenId: c.GetInt("token_id"),
			RequestId: requestID, ErrorType: "new_api_admission_error", ErrorCode: code,
			StatusCode: statusCode, ErrorReason: reason, RequestPath: requestPath, UserAgent: userAgent,
		}
	}
	if constant.ErrorLogEnabled {
		other := map[string]any{
			"error_type": "new_api_admission_error", "error_code": code,
			"status_code": statusCode, "request_path": requestPath,
			"stage": "relay_admission",
			"admin_info": map[string]any{
				"admission": diagnostic, "user_agent": userAgent,
				"access_url": admissionErrorText(common.GetRequestOrigin(c.Request), 512),
			},
		}
		event.usage = &model.Log{
			UserId: c.GetInt("id"), Username: username, CreatedAt: now,
			Type: model.LogTypeError, ModelName: modelName, Group: group,
			TokenId: c.GetInt("token_id"), TokenName: admissionErrorText(c.GetString("token_name"), 128),
			RequestId: requestID, Content: fmt.Sprintf("status_code=%d, %s", statusCode, admissionErrorText(message, 2048)),
			Other: common.GetJsonString(other),
		}
		if common.RequestIPLogEnabled {
			event.usage.Ip = c.ClientIP()
		}
	}
	if !admissionErrors.enqueue(event) {
		logger.LogWarn(c, "admission_error_queue_full request_id="+requestID+"; retained in relay_admission_rejected runtime log")
	}
}
