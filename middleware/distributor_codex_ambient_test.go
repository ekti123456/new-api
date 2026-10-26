package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
)

func codexAmbientSuggestionContext(userID, tokenID int) (*gin.Context, *httptest.ResponseRecorder) {
	const threadID = "01a075e6-d1d1-7170-a523-076c03592fda"
	requestContext, recorder := codexUnlinkedTitleContext(userID, tokenID, threadID)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-terra","input":"Generate suggestions for this project"}`))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	requestContext.Request.Header.Set("Session-Id", threadID)
	requestContext.Request.Header.Set("X-Codex-Installation-Id", "ambient-test-device")
	requestContext.Request.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"`+threadID+`","thread_id":"`+threadID+`","turn_id":"01a075e6-d2a2-7a61-8047-f3611e0acd17","thread_source":"ambient_suggestions","request_kind":"turn"}`)
	return requestContext, recorder
}
