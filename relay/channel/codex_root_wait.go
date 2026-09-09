package channel

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const CodexRootAccountWaitTimeout = 30 * time.Second
const codexRootAccountWaitDeadlineKey = "codex_root_account_wait_deadline"

func CodexRequestNeedsRootAccountWait(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "thread_title", "ambient_suggestions":
		return true
	default:
		return false
	}
}

func StartCodexRootAccountWait(requestContext *gin.Context, source string) {
	if requestContext == nil || !CodexRequestNeedsRootAccountWait(source) || !CodexRootAccountWaitDeadline(requestContext).IsZero() {
		return
	}
	requestContext.Set(codexRootAccountWaitDeadlineKey, time.Now().Add(CodexRootAccountWaitTimeout))
}

func CodexRootAccountWaitDeadline(requestContext *gin.Context) time.Time {
	if requestContext == nil {
		return time.Time{}
	}
	value, _ := requestContext.Get(codexRootAccountWaitDeadlineKey)
	deadline, _ := value.(time.Time)
	return deadline
}

func codexRootAccountWaitMillis(requestContext *gin.Context) *int64 {
	deadline := CodexRootAccountWaitDeadline(requestContext)
	if deadline.IsZero() {
		return nil
	}
	remaining := max(time.Until(deadline).Milliseconds(), 0)
	return &remaining
}
