package channel

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const CodexRootAccountWaitTimeout = 30 * time.Second
const codexRootAccountWaitDeadlineKey = "codex_root_account_wait_deadline"

type CodexRootAssociation struct {
	OriginalRootID string
	Basis          string
	Candidates     int
}

func RecordCodexRootAssociation(requestContext *gin.Context, originalRootID, basis string, candidates int) {
	if requestContext == nil {
		return
	}
	association := CodexRequestRootAssociation(requestContext)
	if association.OriginalRootID == "" {
		association.OriginalRootID = strings.TrimSpace(originalRootID)
	}
	association.Basis = basis
	association.Candidates = candidates
	requestContext.Set("codex_root_association", association)
}

func CodexRequestRootAssociation(requestContext *gin.Context) CodexRootAssociation {
	if requestContext == nil {
		return CodexRootAssociation{}
	}
	value, _ := requestContext.Get("codex_root_association")
	association, _ := value.(CodexRootAssociation)
	return association
}

func CodexRequestNeedsRootAccountWait(source string) bool {
	source = strings.TrimSpace(source)
	return source != "" && !strings.EqualFold(source, "user")
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
