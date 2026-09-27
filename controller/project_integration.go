package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/gin-gonic/gin"
)

func GetCodexProjectSummary(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	now := time.Now()
	scope, err := channel.VerifyProjectSummary(c.Request, now)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "project integration authentication failed"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	rows, err := model.ProjectIntegrationChannels(ctx)
	if err != nil {
		c.JSON(503, gin.H{"error": "channel summary unavailable"})
		return
	}
	ids := make([]int, 0)
	channels := make([]gin.H, 0)
	for _, row := range rows {
		if !scope.Matches(row.GetBaseURL(), row.GetKeys()) {
			continue
		}
		ids = append(ids, row.Id)
		channels = append(channels, gin.H{"id": row.Id, "name": row.Name, "status": row.Status})
	}
	usage, err := model.GetProjectIntegrationUsage(ctx, ids, scope.DayStart, now.Unix())
	if err != nil {
		c.JSON(503, gin.H{"error": "usage summary unavailable"})
		return
	}
	if common.QuotaPerUnit <= 0 {
		c.JSON(503, gin.H{"error": "quota unit unavailable"})
		return
	}
	c.JSON(200, gin.H{"version": 1, "observed_at": now.UTC(), "day_start": scope.DayStart,
		"channels": channels, "rpm": usage.RPM, "requests": usage.Requests,
		"quota": usage.Quota, "quota_per_unit": common.QuotaPerUnit, "usage_usd": float64(usage.Quota) / common.QuotaPerUnit,
		"scope": "audit_binding_channels", "rpm_basis": "completed_consume_logs", "mixed_key_channels": "excluded"})
}
