package common

import (
	"github.com/gin-gonic/gin"
	"sync"
)

const codexUpstreamErrorKey = "verified_codex_upstream_error"

type CodexUpstreamError struct {
	RequestID         string `json:"request_id"`
	ChannelID         int    `json:"channel_id"`
	IssuedAt          int64  `json:"issued_at"`
	Message           string `json:"message"`
	Code              string `json:"code,omitempty"`
	Type              string `json:"type,omitempty"`
	Source            string `json:"source"`
	Stage             string `json:"stage"`
	Transport         string `json:"transport,omitempty"`
	HTTPStatus        int    `json:"http_status,omitempty"`
	HandshakeStatus   int    `json:"handshake_status,omitempty"`
	UpstreamRequestID string `json:"upstream_request_id,omitempty"`
	GatewayRequestID  string `json:"gateway_request_id,omitempty"`
}

// Each outbound attempt owns its recorder. A late response from an earlier
// channel cannot write diagnostics into the current retry's log.
type CodexUpstreamErrorAttempt struct {
	mu         sync.RWMutex
	diagnostic CodexUpstreamError
	present    bool
}

func CodexUpstreamErrorAttemptForContext(c *gin.Context) *CodexUpstreamErrorAttempt {
	if c == nil {
		return nil
	}
	value, _ := c.Get(codexUpstreamErrorKey)
	if attempt, ok := value.(*CodexUpstreamErrorAttempt); ok {
		return attempt
	}
	attempt := &CodexUpstreamErrorAttempt{}
	c.Set(codexUpstreamErrorKey, attempt)
	return attempt
}

func ClearCodexUpstreamError(c *gin.Context) {
	if c != nil {
		c.Set(codexUpstreamErrorKey, &CodexUpstreamErrorAttempt{})
	}
}

func (attempt *CodexUpstreamErrorAttempt) Record(diagnostic CodexUpstreamError) {
	if attempt == nil {
		return
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	attempt.diagnostic, attempt.present = diagnostic, true
}

func GetCodexUpstreamError(c *gin.Context, channelID int) (CodexUpstreamError, bool) {
	if c == nil {
		return CodexUpstreamError{}, false
	}
	value, _ := c.Get(codexUpstreamErrorKey)
	attempt, ok := value.(*CodexUpstreamErrorAttempt)
	if !ok {
		return CodexUpstreamError{}, false
	}
	attempt.mu.RLock()
	defer attempt.mu.RUnlock()
	diagnostic := attempt.diagnostic
	return diagnostic, attempt.present && diagnostic.RequestID == c.GetString(RequestIdKey) && diagnostic.ChannelID == channelID
}
