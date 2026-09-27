package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func appendCodexUpstreamError(c *gin.Context, channelID int, other *LogOther) *LogOther {
	if other == nil {
		other = NewLogOther()
	}
	if diagnostic, ok := common.GetCodexUpstreamError(c, channelID); ok {
		other.SetAdmin("upstream_error", diagnostic)
	}
	if diagnostic, ok := common.GetCodexDispatchDiagnostic(c, channelID, 503); ok {
		other.SetAdmin("dispatch_diagnostic", diagnostic)
	}
	return other
}
