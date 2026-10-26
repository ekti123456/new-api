package middleware

import (
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
)

func independentCodexBackgroundContext(userID, tokenID int, threadID, turnID string) (*gin.Context, *httptest.ResponseRecorder) {
	requestContext, recorder := codexMainRootTurnContext(userID, tokenID, 0, threadID, turnID)
	metadata := strings.ReplaceAll(requestContext.GetHeader("X-Codex-Turn-Metadata"), `"thread_source":"user"`, `"thread_source":"agent_created_thread"`)
	requestContext.Request.Header.Set("X-Codex-Turn-Metadata", metadata)
	requestContext.Request.Header.Set("X-Codex-Installation-Id", "strict-background-device")
	return requestContext, recorder
}
