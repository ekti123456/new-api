package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/requestlimit"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var independentRateEditorMu sync.Mutex

type independentRateEditor struct {
	Settings setting.IndependentRateLimits `json:"settings"`
	Revision string                        `json:"revision"`
}

func GetIndependentRateLimits(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	s, revision, err := setting.ReadIndependentRateLimits()
	if err != nil {
		common.ApiErrorMsg(c, "Invalid saved independent rate limits")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": independentRateEditor{Settings: *s, Revision: revision}})
}

func SaveIndependentRateLimits(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input independentRateEditor
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20), &input) != nil {
		common.ApiErrorMsg(c, "Invalid independent rate limits")
		return
	}
	independentRateEditorMu.Lock()
	defer independentRateEditorMu.Unlock()
	old, revision, err := setting.ReadIndependentRateLimits()
	if err != nil {
		common.ApiErrorMsg(c, "Invalid saved independent rate limits")
		return
	}
	if revision != input.Revision {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Settings changed; reload before saving"})
		return
	}
	input.Settings.Namespace = old.Namespace
	if input.Settings.Namespace == "" {
		input.Settings.Namespace = uuid.NewString()
	}
	if err := input.Settings.Validate(); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	userIDs := map[int]bool{}
	for _, rule := range input.Settings.Rules {
		for _, override := range rule.Overrides {
			userIDs[override.UserID] = true
		}
	}
	if len(userIDs) > 0 {
		ids := make([]int, 0, len(userIDs))
		for id := range userIDs {
			ids = append(ids, id)
		}
		var count int64
		if err := model.DB.Model(&model.User{}).Where("id IN ?", ids).Count(&count).Error; err != nil || count != int64(len(ids)) {
			common.ApiErrorMsg(c, "One or more override users do not exist")
			return
		}
	}
	raw, err := common.Marshal(input.Settings)
	if err != nil {
		common.ApiErrorMsg(c, "Cannot encode rate limits")
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{setting.IndependentRateLimitOption: string(raw)}); err != nil {
		common.ApiErrorMsg(c, "Cannot save rate limits")
		return
	}
	GetIndependentRateLimits(c)
}

func GetIndependentRateUsage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	s, _, err := setting.ReadIndependentRateLimits()
	if err != nil {
		common.ApiErrorMsg(c, "Invalid saved independent rate limits")
		return
	}
	var rule *setting.IndependentRateRule
	for i := range s.Rules {
		if s.Rules[i].ID == c.Query("rule_id") {
			rule = &s.Rules[i]
			break
		}
	}
	if rule == nil {
		c.JSON(404, gin.H{"success": false, "message": "Rate rule not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	ids, err := requestlimit.Default.ActiveUsers(ctx, s.Namespace, rule.ID)
	if err != nil {
		c.JSON(503, gin.H{"success": false, "message": "Rate limit store unavailable"})
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	page = max(1, page)
	size, _ := strconv.Atoi(c.Query("size"))
	if size <= 0 {
		size = 20
	}
	size = min(size, 100)
	type row struct {
		UserID      int    `json:"user_id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		requestlimit.Usage
	}
	rows := []row{}
	var total int64
	if len(ids) > 0 {
		query := model.DB.WithContext(ctx).Model(&model.User{}).Where("id IN ?", ids)
		if search := strings.TrimSpace(c.Query("query")); search != "" {
			id, _ := strconv.Atoi(search)
			query = query.Where("id = ? OR username LIKE ? OR display_name LIKE ?", id, "%"+search+"%", "%"+search+"%")
		}
		if err := query.Count(&total).Error; err != nil {
			common.ApiErrorMsg(c, "Cannot load users")
			return
		}
		if int64(page-1) <= total/int64(size) {
			if err := query.Select("id AS user_id, username, display_name").Order("id ASC").Offset((page - 1) * size).Limit(size).Scan(&rows).Error; err != nil {
				common.ApiErrorMsg(c, "Cannot load users")
				return
			}
		}
		for i := range rows {
			rows[i].Usage, err = requestlimit.Default.Check(ctx, s.Namespace, rule.ID, rows[i].UserID, rule.UserLimit(rows[i].UserID), false)
			if err != nil {
				common.ApiErrorMsg(c, "Rate limit store unavailable")
				return
			}
		}
	}
	backend := "memory"
	if common.RedisEnabled {
		backend = "redis"
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"items": rows, "total": total, "page": page, "size": size, "backend": backend, "enabled": s.Enabled && rule.Enabled}})
}
