package common

import (
	"sync"

	"github.com/gin-gonic/gin"
)

const codexDispatchDiagnosticKey = "verified_codex_dispatch_diagnostic"

type CodexDispatchDiagnostic struct {
	RequestID   string   `json:"request_id"`
	ChannelID   int      `json:"channel_id"`
	Status      int      `json:"status"`
	IssuedAt    int64    `json:"issued_at"`
	Stage       string   `json:"stage"`
	Reason      string   `json:"reason"`
	Reasons     []string `json:"reasons"`
	RootAccount int64    `json:"root_account,omitempty"`
	Retry       string   `json:"retry"`
	Incomplete  bool     `json:"incomplete,omitempty"`
	Stream      bool     `json:"-"`
}

type CodexDispatchAttempt struct {
	mu         sync.RWMutex
	diagnostic CodexDispatchDiagnostic
	present    bool
}

func CodexDispatchAttemptForContext(ctx *gin.Context) *CodexDispatchAttempt {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Get(codexDispatchDiagnosticKey)
	if attempt, ok := value.(*CodexDispatchAttempt); ok {
		return attempt
	}
	attempt := &CodexDispatchAttempt{}
	ctx.Set(codexDispatchDiagnosticKey, attempt)
	return attempt
}

func (attempt *CodexDispatchAttempt) Record(diagnostic CodexDispatchDiagnostic) {
	if attempt == nil {
		return
	}
	diagnostic.Reasons = append([]string(nil), diagnostic.Reasons...)
	attempt.mu.Lock()
	attempt.diagnostic = diagnostic
	attempt.present = true
	attempt.mu.Unlock()
}

func SetCodexDispatchDiagnostic(ctx *gin.Context, diagnostic CodexDispatchDiagnostic) {
	CodexDispatchAttemptForContext(ctx).Record(diagnostic)
}

func ClearCodexDispatchDiagnostic(ctx *gin.Context) {
	if ctx != nil {
		ctx.Set(codexDispatchDiagnosticKey, &CodexDispatchAttempt{})
	}
}

func GetCodexDispatchDiagnostic(ctx *gin.Context, channelID, status int) (CodexDispatchDiagnostic, bool) {
	if ctx == nil {
		return CodexDispatchDiagnostic{}, false
	}
	value, _ := ctx.Get(codexDispatchDiagnosticKey)
	attempt, ok := value.(*CodexDispatchAttempt)
	if !ok {
		return CodexDispatchDiagnostic{}, false
	}
	attempt.mu.RLock()
	defer attempt.mu.RUnlock()
	diagnostic := attempt.diagnostic
	if !attempt.present || diagnostic.RequestID != ctx.GetString(RequestIdKey) || diagnostic.ChannelID != channelID || diagnostic.Status != status {
		return CodexDispatchDiagnostic{}, false
	}
	diagnostic.Reasons = append([]string(nil), diagnostic.Reasons...)
	return diagnostic, true
}
