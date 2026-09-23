package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func appendCodexUpstreamError(c *gin.Context, channelID int, other map[string]interface{}) map[string]interface{} {
	diagnostic, ok := common.GetCodexUpstreamError(c, channelID)
	if !ok {
		return other
	}
	if other == nil {
		other = make(map[string]interface{})
	}
	admin, _ := other["admin_info"].(map[string]interface{})
	if admin == nil {
		admin = make(map[string]interface{})
		other["admin_info"] = admin
	}
	admin["upstream_error"] = diagnostic
	return other
}
