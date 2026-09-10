package middleware

import (
	"github.com/QuantumNous/new-api/common"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const relayAdmissionModelKey = "relay_admission_model"
const relayAdmissionResolutionKey = "relay_admission_resolution"

func recordRelayAdmissionError(c *gin.Context, statusCode int, code, message string) {
	if c.GetBool("relay_admission_error_recorded") {
		return
	}
	c.Set("relay_admission_error_recorded", true)
	modelName := c.GetString(relayAdmissionModelKey)
	if modelName == "" {
		modelName = c.GetString("original_model")
	}
	if modelName == "" {
		modelName = c.Query("model")
	}
	metadata := map[string]string{"metadata_source": "headers_only"}
	if raw, exists := c.Get(relayAdmissionResolutionKey); exists {
		if resolution, ok := raw.(relaychannel.CodexRootSessionResolution); ok {
			metadata = map[string]string{
				"metadata_source": "parsed_request",
				"thread_source":   resolution.ThreadSource,
				"request_kind":    resolution.RequestKind,
				"subagent_kind":   resolution.SubagentKind,
				"thread_id":       resolution.ThreadID,
				"root_id":         resolution.RootID,
				"window_id":       resolution.WindowID,
			}
		}
	}
	if metadata["metadata_source"] == "headers_only" {
		header := c.GetHeader("X-Codex-Turn-Metadata")
		if len(header) <= 8192 && gjson.Valid(header) {
			for _, field := range []string{"thread_source", "request_kind", "subagent_kind", "session_id", "thread_id", "parent_thread_id", "root_turn_id", "window_id"} {
				value := gjson.Get(header, field)
				if value.Type == gjson.String {
					metadata[field] = value.String()
				}
			}
		}
		metadata["window_id"] = common.GetStringIfEmpty(metadata["window_id"], c.GetHeader("X-Codex-Window-Id"))
		metadata["thread_id"] = common.GetStringIfEmpty(metadata["thread_id"], c.GetHeader("Thread-Id"))
		metadata["session_id"] = common.GetStringIfEmpty(metadata["session_id"], c.GetHeader("Session-Id"))
		metadata["subagent_kind"] = common.GetStringIfEmpty(metadata["subagent_kind"], c.GetHeader("X-OpenAI-Subagent"))
	}
	perfmetrics.RecordAdmissionError(c, modelName, statusCode, code, message, metadata)
}
