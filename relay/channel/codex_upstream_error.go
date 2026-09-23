package channel

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexUpstreamErrorHeader = "X-Codex2API-Error-Diagnostic"
const codexUpstreamErrorDomain = "codex2api:error-diagnostic:v1"

// Forward normal SSE bytes immediately. Only the event following a protected
// diagnostic needs bounded inspection, since response.failed can include output
// much larger than the reader's 4 KiB buffer.
func (body *codexDispatchStreamBody) observeUpstreamErrorLine(line []byte, continued bool) {
	if !continued && bytes.HasPrefix(line, []byte(": codex2api_error")) {
		body.errorEncoded, body.errorData = "", nil
		if !body.continuation && bytes.HasPrefix(line, []byte(": codex2api_error ")) {
			encoded := strings.TrimSpace(string(line[len(": codex2api_error "):]))
			if len(encoded) <= 3000 {
				body.errorEncoded = encoded
			}
		}
		return
	}
	if body.errorEncoded == "" {
		return
	}
	if !continued {
		if !bytes.HasPrefix(line, []byte("data:")) {
			return
		}
		body.errorData = nil
	} else if body.errorData == nil {
		return
	}
	if len(body.errorData)+len(line) > 1024*1024 {
		body.errorEncoded, body.errorData = "", nil
		return
	}
	if body.continuation || continued {
		body.errorData = append(body.errorData, line...)
		if body.continuation {
			return
		}
		line = body.errorData
	}
	if codexUpstreamFailurePayload(bytes.TrimSpace(line[5:])) {
		if diagnostic, err := decodeCodexUpstreamError(body.errorEncoded, body.request, time.Now()); err == nil {
			body.request.ErrorAttempt.Record(diagnostic)
		}
	}
	body.errorEncoded, body.errorData = "", nil
}

func decodeCodexUpstreamError(encoded string, request newAPIPolicyRequestContext, now time.Time) (common.CodexUpstreamError, error) {
	var diagnostic common.CodexUpstreamError
	if request.Secret == "" || request.RequestID == "" || request.UserID <= 0 || request.ChannelID <= 0 || len(encoded) > 3000 || !strings.HasPrefix(encoded, "v1.") {
		return diagnostic, fmt.Errorf("invalid error diagnostic envelope")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "v1."))
	if err != nil {
		return diagnostic, fmt.Errorf("invalid error diagnostic encoding")
	}
	key := hmac.New(sha256.New, []byte(request.Secret))
	key.Write([]byte(codexUpstreamErrorDomain))
	block, err := aes.NewCipher(key.Sum(nil))
	if err != nil {
		return diagnostic, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return diagnostic, err
	}
	if len(sealed) != aead.NonceSize()+2048+aead.Overhead() {
		return diagnostic, fmt.Errorf("invalid error diagnostic length")
	}
	aad := strings.Join([]string{codexUpstreamErrorDomain, request.RequestID, strconv.Itoa(request.UserID), request.PlatformID}, "\n")
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(aad))
	if err != nil {
		return diagnostic, fmt.Errorf("invalid error diagnostic authentication")
	}
	size := int(binary.BigEndian.Uint16(plaintext))
	if size == 0 || size > len(plaintext)-2 || common.Unmarshal(plaintext[2:2+size], &diagnostic) != nil {
		return diagnostic, fmt.Errorf("invalid error diagnostic payload")
	}
	if diagnostic.RequestID != request.RequestID || diagnostic.ChannelID != request.ChannelID || diagnostic.IssuedAt < now.Unix()-60 || diagnostic.IssuedAt > now.Unix()+10 || diagnostic.Message == "" || len(diagnostic.Message) > 1000 || len(diagnostic.Code) > 128 || len(diagnostic.Type) > 128 || len(diagnostic.Source) > 64 || len(diagnostic.Stage) > 64 || len(diagnostic.Transport) > 32 || len(diagnostic.UpstreamRequestID) > 128 || len(diagnostic.GatewayRequestID) > 128 {
		return diagnostic, fmt.Errorf("invalid error diagnostic scope")
	}
	for _, status := range []int{diagnostic.HTTPStatus, diagnostic.HandshakeStatus} {
		if status != 0 && (status < 100 || status > 599) {
			return diagnostic, fmt.Errorf("invalid upstream status")
		}
	}
	return diagnostic, nil
}

func processCodexUpstreamErrorHeader(resp *http.Response, request newAPIPolicyRequestContext) {
	if resp == nil {
		return
	}
	encoded := resp.Header.Get(codexUpstreamErrorHeader)
	for name := range resp.Header {
		if strings.EqualFold(name, codexUpstreamErrorHeader) {
			delete(resp.Header, name)
		}
	}
	if resp.StatusCode < 400 || resp.StatusCode > 599 || encoded == "" {
		return
	}
	if diagnostic, err := decodeCodexUpstreamError(encoded, request, time.Now()); err == nil {
		request.ErrorAttempt.Record(diagnostic)
	}
}

func codexUpstreamFailurePayload(payload []byte) bool {
	value := gjson.ParseBytes(payload)
	if !value.IsObject() {
		return false
	}
	kind := value.Get("type").String()
	return (kind == "response.failed" && value.Get("response.error").IsObject()) || ((kind == "error" || kind == "") && value.Get("error").IsObject()) || (kind == "error" && value.Get("message").Type == gjson.String)
}

func sanitizeCodexUpstreamErrorWebSocketMessage(c *gin.Context, message []byte) []byte {
	var request newAPIPolicyRequestContext
	if c != nil {
		value, _ := c.Get(newAPIPolicyRequestContextGinKey)
		request, _ = value.(newAPIPolicyRequestContext)
	}
	for _, path := range []string{"error.details.codex2api_error", "response.error.details.codex2api_error", "details.codex2api_error"} {
		carrier := gjson.GetBytes(message, path)
		if !carrier.Exists() {
			continue
		}
		encoded := carrier.String()
		if sanitized, err := sjson.DeleteBytes(message, path); err == nil {
			message = sanitized
		} else {
			return []byte(`{"type":"error","error":{"code":"upstream_error","message":"Upstream request failed"}}`)
		}
		if codexUpstreamFailurePayload(message) {
			if diagnostic, err := decodeCodexUpstreamError(encoded, request, time.Now()); err == nil {
				request.ErrorAttempt.Record(diagnostic)
			}
		}
	}
	return message
}
