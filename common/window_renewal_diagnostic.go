package common

import "github.com/gin-gonic/gin"

const windowRenewalDiagnosticKey = "codex_window_renewal_diagnostic"

// Request correlation only: never store tickets, identities or request bodies.
type WindowRenewalDiagnostic struct {
	FirstRequestID string `json:"first_request_id"`
	RetryRequestID string `json:"retry_request_id,omitempty"`
	Result         string `json:"result"`
	FinalCode      string `json:"final_code,omitempty"`
	FinalStatus    int    `json:"final_status,omitempty"`
	ChannelID      int    `json:"channel_id"`
	Renewals       int    `json:"renewals"`
}

func BeginWindowRenewalDiagnostic(c *gin.Context, channelID int) *WindowRenewalDiagnostic {
	if d := WindowRenewalForRequest(c); d != nil {
		return d
	}
	d := &WindowRenewalDiagnostic{FirstRequestID: c.GetString(RequestIdKey), ChannelID: channelID, Result: "refresh_required"}
	c.Set(windowRenewalDiagnosticKey, d)
	return d
}

func WindowRenewalForRequest(c *gin.Context) *WindowRenewalDiagnostic {
	if c == nil {
		return nil
	}
	v, _ := c.Get(windowRenewalDiagnosticKey)
	d, _ := v.(*WindowRenewalDiagnostic)
	return d
}
