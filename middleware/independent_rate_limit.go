package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/requestlimit"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// Returns false without touching the body or counters when the independent
// pool is disabled, or no original path/UA matches. This runs after TokenAuth.
func applyIndependentRequestLimit(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost || c.GetInt("id") <= 0 {
		return false
	}
	settings, _, err := setting.ReadIndependentRateLimits()
	if err != nil || !settings.Enabled {
		return false
	}
	var candidates []setting.IndependentRateRule
	for _, rule := range settings.Rules {
		if rule.MatchesRequest(c.Request.URL.Path, c.Request.UserAgent()) {
			candidates = append(candidates, rule)
		}
	}
	if len(candidates) == 0 {
		return false
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false
	}
	var body struct {
		Stream *bool  `json:"stream"`
		Model  string `json:"model"`
	}
	err = common.DecodeJson(storage, &body)
	_, seekErr := storage.Seek(0, io.SeekStart)
	c.Request.Body = io.NopCloser(storage)
	if err != nil || seekErr != nil {
		return false
	}
	stream := body.Stream != nil && *body.Stream
	for _, rule := range candidates {
		if rule.Stream == "stream" && !stream || rule.Stream == "non_stream" && stream {
			continue
		}
		userID := c.GetInt("id")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		usage, checkErr := requestlimit.Default.Check(ctx, settings.Namespace, rule.ID, userID, rule.UserLimit(userID), true)
		cancel()
		if checkErr != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "server_error", "code": "independent_rate_limit_unavailable", "message": "Request rate limit service is temporarily unavailable"}})
			return true
		}
		if usage.Allowed {
			c.Next()
			return true
		}
		c.Header("Retry-After", strconv.Itoa(usage.RetryAfter))
		c.Header("X-RateLimit-Limit", strconv.Itoa(usage.Limit))
		c.Header("X-RateLimit-Remaining", "0")
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"type": "rate_limit_error", "code": "independent_request_rate_limit", "message": fmt.Sprintf("Request rate limit exceeded: %d requests per minute for this request type", usage.Limit)}})
		if model.DB != nil && model.LOG_DB != nil {
			other := model.NewLogOther()
			other.SetPublic("error_code", "independent_request_rate_limit")
			other.SetAdmin("independent_rate_limit", map[string]any{"rule_id": rule.ID, "rule_name": rule.Name, "count": usage.Count, "limit": usage.Limit, "retry_after": usage.RetryAfter})
			model.RecordErrorLog(c, userID, 0, body.Model, c.GetString("token_name"), "Independent request rate limit exceeded", c.GetInt(string(constant.ContextKeyTokenId)), 0, stream, common.GetContextKeyString(c, constant.ContextKeyTokenGroup), other)
		}
		return true
	}
	return false
}
