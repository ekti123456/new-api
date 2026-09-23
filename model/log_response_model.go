package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func appendUpstreamResponseModel(c *gin.Context, other map[string]interface{}) map[string]interface{} {
	// Never copy the mapped/requested model as a fallback, or accept a value
	// manufactured by a converter/billing helper.
	delete(other, "upstream_response_model")
	delete(other, "upstream_response_model_conflict")
	name, conflict := common.GetUpstreamResponseModel(c)
	if name == "" {
		return other
	}
	if other == nil {
		other = make(map[string]interface{})
	}
	other["upstream_response_model"] = name
	if conflict {
		other["upstream_response_model_conflict"] = true
	}
	return other
}
