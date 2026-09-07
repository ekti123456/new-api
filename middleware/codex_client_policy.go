package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/i18n"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func rejectRestrictedCodexPassiveModel(c *gin.Context, modelName string, resolution relaychannel.CodexRootSessionResolution) bool {
	if !strings.EqualFold(strings.TrimSpace(modelName), "gpt-5.4") {
		return false
	}
	source := strings.TrimSpace(resolution.ThreadSource)
	if (source == "" || strings.EqualFold(source, "user")) && strings.TrimSpace(resolution.SubagentKind) == "" && relaychannel.CodexPassiveRootSessionOverrideFeature(c) == "" {
		return false
	}
	abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgCodexPassiveModelRestricted), types.ErrorCode("codex_passive_model_restricted"))
	return true
}
