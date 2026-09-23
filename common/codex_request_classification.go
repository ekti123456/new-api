package common

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const codexRequestClassificationKey = "codex_request_classification"
const CodexProtocolCompactionKey = "codex_protocol_compaction"

// Diagnostic protocol labels only. No prompt, credentials or session IDs.
type CodexRequestClassification struct {
	Type         string `json:"type"`
	IngressType  string `json:"ingress_type"`
	ThreadSource string `json:"thread_source,omitempty"`
	RequestKind  string `json:"request_kind,omitempty"`
	SubagentKind string `json:"subagent_kind,omitempty"`
	RootState    string `json:"root_state,omitempty"`
	Related      bool   `json:"related"`
}

func ObserveCodexRequestClassification(c *gin.Context, source, kind, subagent, rootState string, related bool) {
	if c == nil {
		return
	}
	label := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		if len(value) > 128 {
			return ""
		}
		for _, r := range value {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.", r)) {
				return ""
			}
		}
		return value
	}
	observation := CodexRequestClassification{Type: "unknown", ThreadSource: label(source), RequestKind: label(kind), SubagentKind: label(subagent), RootState: label(rootState), Related: related}
	compactEndpoint := c.Request != nil && c.Request.URL != nil && strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/responses/compact")
	switch {
	case observation.RequestKind == "compaction" || compactEndpoint || c.GetBool(CodexProtocolCompactionKey):
		observation.Type = "compaction"
		observation.RequestKind = "compaction"
	case observation.ThreadSource == "" && strings.TrimSpace(source) != "":
	case observation.RootState == "conflict":
		// Conflicting identifiers must not be displayed as a verified relation.
	case observation.ThreadSource != "" && observation.ThreadSource != "user":
		observation.Type = "independent_internal"
		if related {
			observation.Type = "related_internal"
		}
	case observation.ThreadSource == "user" || observation.RootState == "resolved" && !related:
		observation.Type = "user"
	case related:
		observation.Type = "related_unclassified"
	}
	observation.IngressType = observation.Type
	if previous, ok := GetCodexRequestClassification(c); ok {
		observation.IngressType = previous.IngressType
	}
	c.Set(codexRequestClassificationKey, observation)
}

func GetCodexRequestClassification(c *gin.Context) (CodexRequestClassification, bool) {
	if c == nil {
		return CodexRequestClassification{}, false
	}
	value, exists := c.Get(codexRequestClassificationKey)
	observation, ok := value.(CodexRequestClassification)
	return observation, exists && ok
}
