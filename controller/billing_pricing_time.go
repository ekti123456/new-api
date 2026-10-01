package controller

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
)

// GetBillingPricingTime shares the actual billing calendar with the editor's
// local estimator. The browser must not infer holidays from its own clock.
func GetBillingPricingTime(c *gin.Context) {
	at := time.Now()
	band, _, err := billingexpr.RunExprWithRequest("cn_off_peak() ? 1 : 0", billingexpr.TokenParams{}, billingexpr.RequestInput{PricingTime: at})
	if err != nil {
		common.ApiErrorMsg(c, "Pricing time is unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"cn_off_peak": band == 1, "evaluated_at": at.UTC().Format(time.RFC3339)}})
}
