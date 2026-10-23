package controller

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func policyRestrictionFailure(c *gin.Context, err error) {
	logger.LogError(c.Request.Context(), "policy restriction administration failed: "+err.Error())
	c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to query or update restrictions. Reload and try again."})
}

func policyRestrictionQuery(c *gin.Context) (string, int, bool) {
	ip := strings.TrimSpace(c.Query("ip"))
	if ip != "" {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			c.JSON(400, gin.H{"success": false, "message": "Enter a valid IP address"})
			return "", 0, false
		}
		ip = parsed.String()
	}
	userID := 0
	if raw := c.Query("user_id"); raw != "" {
		var err error
		userID, err = strconv.Atoi(raw)
		if err != nil || userID < 1 {
			c.JSON(400, gin.H{"success": false, "message": "Enter a valid user ID"})
			return "", 0, false
		}
	}
	return ip, userID, true
}

func ListCodex2APIPolicyIPBlocks(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	ip, userID, ok := policyRestrictionQuery(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 || page > 100000 {
		c.JSON(400, gin.H{"success": false, "message": "Invalid page"})
		return
	}
	status := c.DefaultQuery("status", "active")
	if status != "active" && status != "expired" && status != "revoked" && status != "all" {
		c.JSON(400, gin.H{"success": false, "message": "Invalid restriction status"})
		return
	}
	config, err := common.ReadCodexPolicyIPEnforcement()
	if err != nil {
		policyRestrictionFailure(c, err)
		return
	}
	now := time.Now().Unix()
	rows, total, err := model.ListCodexPolicyIPBlocks(ip, userID, status, (page-1)*50, 50, now)
	if err != nil {
		policyRestrictionFailure(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"items": rows, "total": total, "page": page, "page_size": 50, "server_time": now, "enforcement": config}})
}

func GetCodex2APIPolicyRestrictions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	ip, userID, ok := policyRestrictionQuery(c)
	if !ok {
		return
	}
	if ip == "" && userID == 0 {
		c.JSON(400, gin.H{"success": false, "message": "Enter an IP address or user ID"})
		return
	}
	config, err := common.ReadCodexPolicyIPEnforcement()
	if err != nil {
		policyRestrictionFailure(c, err)
		return
	}
	data := gin.H{"enforcement": config, "server_time": time.Now().Unix()}
	if ip != "" {
		var block model.CodexPolicyIPBlock
		if err := model.DB.Where("ip = ?", ip).Find(&block).Error; err != nil {
			policyRestrictionFailure(c, err)
			return
		}
		if userID == 0 {
			userID = block.UserID
		}
		data["ip_block"] = block
		data["ip"] = ip
		limiter, err := middleware.GetCriticalRateLimitStatus(c.Request.Context(), ip)
		if err != nil {
			// Preserve useful IP/account evidence if Redis is unavailable, while
			// making clear that the limiter's status is unknown, not unblocked.
			data["login_limit_error"] = true
			logger.LogError(c.Request.Context(), "login limiter inspection failed: "+err.Error())
		} else {
			data["login_limit"] = limiter
		}
	}
	if userID > 0 {
		settings, _, _, err := channel.ReadNewAPIPolicySettings()
		if err != nil {
			policyRestrictionFailure(c, err)
			return
		}
		detail, err := model.GetCodexPolicyRestrictions(userID, settings.WindowSeconds, time.Now().Unix())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"success": false, "message": "User not found"})
			return
		}
		if err != nil {
			policyRestrictionFailure(c, err)
			return
		}
		data["user"] = detail
	}
	c.JSON(200, gin.H{"success": true, "data": data})
}

func ReleaseCodex2APIPolicyRestrictions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input model.CodexPolicyRelease
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10), &input); err != nil {
		c.JSON(400, gin.H{"success": false, "message": "Invalid restriction release"})
		return
	}
	err := model.ReleaseCodexPolicyRestrictions(input, c.GetInt("id"))
	if errors.Is(err, model.ErrInvalidPolicyRelease) {
		c.JSON(400, gin.H{"success": false, "message": "Invalid restriction release"})
		return
	}
	if errors.Is(err, model.ErrPolicyRestrictionsChanged) || errors.Is(err, model.ErrAccountNotPolicyDisabled) {
		c.JSON(409, gin.H{"success": false, "message": "Restrictions changed or account needs manual review. Reload before releasing."})
		return
	}
	if err != nil {
		policyRestrictionFailure(c, err)
		return
	}
	recordManageAuditFor(c, input.UserID, "codex_policy.release", map[string]interface{}{"ips": input.IPs, "restore_account": input.RestoreAccount, "reset_strikes": input.ResetStrikes, "reason": input.Reason})
	c.JSON(200, gin.H{"success": true})
}
