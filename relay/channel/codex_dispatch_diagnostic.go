package channel

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const codexDispatchDiagnosticHeader = "X-Codex2API-Dispatch-Diagnostic"
const codexDispatchDiagnosticDomain = "codex2api:dispatch-diagnostic:v1"
const codexDispatchPlaintextSize = 2048

func decodeCodexDispatchDiagnostic(encoded string, request newAPIPolicyRequestContext, now time.Time) (common.CodexDispatchDiagnostic, error) {
	var diagnostic common.CodexDispatchDiagnostic
	if request.Secret == "" || request.RequestID == "" || request.UserID <= 0 || request.ChannelID <= 0 || len(encoded) > 3000 || !strings.HasPrefix(encoded, "v1.") {
		return diagnostic, fmt.Errorf("invalid diagnostic envelope")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "v1."))
	if err != nil {
		return diagnostic, fmt.Errorf("invalid diagnostic encoding")
	}
	key := hmac.New(sha256.New, []byte(request.Secret))
	key.Write([]byte(codexDispatchDiagnosticDomain))
	block, err := aes.NewCipher(key.Sum(nil))
	if err != nil {
		return diagnostic, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return diagnostic, err
	}
	if len(sealed) != aead.NonceSize()+codexDispatchPlaintextSize+aead.Overhead() {
		return diagnostic, fmt.Errorf("invalid diagnostic length")
	}
	aad := strings.Join([]string{codexDispatchDiagnosticDomain, request.RequestID, strconv.Itoa(request.UserID), request.PlatformID}, "\n")
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(aad))
	if err != nil {
		return diagnostic, fmt.Errorf("invalid diagnostic authentication")
	}
	size := int(binary.BigEndian.Uint16(plaintext))
	if size == 0 || size > len(plaintext)-2 || common.Unmarshal(plaintext[2:2+size], &diagnostic) != nil {
		return diagnostic, fmt.Errorf("invalid diagnostic payload")
	}
	if diagnostic.RequestID != request.RequestID || diagnostic.ChannelID != request.ChannelID || diagnostic.Status != http.StatusServiceUnavailable || diagnostic.Stage != "account_selection" || diagnostic.RootAccount < 0 || diagnostic.IssuedAt < now.Unix()-60 || diagnostic.IssuedAt > now.Unix()+10 || len(diagnostic.Reasons) > 24 {
		return diagnostic, fmt.Errorf("invalid diagnostic scope")
	}
	if diagnostic.Retry != "stop" && diagnostic.Retry != "backoff_same_route" && diagnostic.Retry != "default" {
		return diagnostic, fmt.Errorf("invalid diagnostic retry advice")
	}
	for _, reason := range append(append([]string(nil), diagnostic.Reasons...), diagnostic.Reason) {
		switch reason {
		case "root_unresolved", "root_owner_mismatch", "diagnosis_incomplete", "mixed_constraints", "root_owner_unavailable",
			"account_disabled", "account_paused", "account_error", "account_banned", "account_cooldown",
			"account_usage_exhausted", "credential_unavailable", "account_unavailable", "model_or_provider_mismatch",
			"affinity_group_mismatch", "upstream_channel_mismatch", "compaction_domain_mismatch",
			"scope_budget_exhausted", "scope_concurrency_exhausted", "model_cooldown", "egress_unavailable",
			"api_key_scope_mismatch", "request_excluded", "account_missing", "concurrency_exhausted",
			"indexed_candidates_unavailable", "lazy_account_unavailable", "lazy_refresh_failed", "dispatch_state_changed",
			"pool_empty", "spark_account_unavailable", "session_capacity_exhausted", "lazy_refresh_pending":
		default:
			return diagnostic, fmt.Errorf("invalid diagnostic reason")
		}
	}
	return diagnostic, nil
}

func processCodexDispatchHeader(resp *http.Response, request newAPIPolicyRequestContext) {
	if resp == nil {
		return
	}
	encoded := resp.Header.Get(codexDispatchDiagnosticHeader)
	for name := range resp.Header {
		if strings.EqualFold(name, codexDispatchDiagnosticHeader) {
			delete(resp.Header, name)
		}
	}
	if resp.StatusCode != http.StatusServiceUnavailable || encoded == "" {
		return
	}
	if diagnostic, err := decodeCodexDispatchDiagnostic(encoded, request, time.Now()); err == nil {
		request.DispatchAttempt.Record(diagnostic)
	}
}

type codexDispatchStreamBody struct {
	io.ReadCloser
	reader       *bufio.Reader
	request      newAPIPolicyRequestContext
	output       []byte
	readErr      error
	continuation bool
	discard      bool
	encoded      string
}

func (body *codexDispatchStreamBody) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	for len(body.output) == 0 {
		if body.readErr != nil {
			return 0, body.readErr
		}
		line, err := body.reader.ReadSlice('\n')
		continued := body.continuation
		body.continuation = err == bufio.ErrBufferFull
		if err != nil && err != bufio.ErrBufferFull {
			body.readErr = err
		}
		if !continued {
			body.discard = bytes.HasPrefix(line, []byte(": codex2api_dispatch"))
			if body.discard {
				body.encoded = ""
				if !body.continuation && bytes.HasPrefix(line, []byte(": codex2api_dispatch ")) {
					encoded := strings.TrimSpace(string(line[len(": codex2api_dispatch "):]))
					if len(encoded) <= 3000 {
						body.encoded = encoded
					}
				}
			} else if bytes.HasPrefix(line, []byte("data:")) && body.encoded != "" {
				if !body.continuation && codexDispatchFailurePayload(bytes.TrimSpace(line[5:])) {
					if diagnostic, decodeErr := decodeCodexDispatchDiagnostic(body.encoded, body.request, time.Now()); decodeErr == nil {
						diagnostic.Stream = true
						body.request.DispatchAttempt.Record(diagnostic)
					}
				}
				body.encoded = ""
			}
		}
		if !body.discard {
			body.output = line
		}
	}
	written := copy(output, body.output)
	body.output = body.output[written:]
	return written, nil
}

func codexDispatchFailurePayload(payload []byte) bool {
	var event struct {
		Type  string `json:"type"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"response"`
	}
	if common.Unmarshal(payload, &event) != nil {
		return false
	}
	return ((event.Type == "" || event.Type == "error") && event.Error.Code == "service_unavailable") || (event.Type == "response.failed" && event.Response.Error.Code == "service_unavailable")
}

func SanitizeCodexDispatchWebSocketMessage(ctx *gin.Context, message []byte) []byte {
	if !gjson.GetBytes(message, "error.details.codex2api_dispatch").Exists() {
		return message
	}
	if len(message) > 16384 {
		return []byte(`{"type":"error","error":{"code":"service_unavailable","type":"server_error","message":"Request temporarily unavailable"}}`)
	}
	var event map[string]json.RawMessage
	var apiError map[string]json.RawMessage
	var details map[string]json.RawMessage
	if common.Unmarshal(message, &event) != nil || common.Unmarshal(event["error"], &apiError) != nil || common.Unmarshal(apiError["details"], &details) != nil {
		return []byte(`{"type":"error","error":{"code":"service_unavailable","type":"server_error","message":"Request temporarily unavailable"}}`)
	}
	var encoded string
	_ = common.Unmarshal(details["codex2api_dispatch"], &encoded)
	delete(details, "codex2api_dispatch")
	apiError["details"], _ = common.Marshal(details)
	event["error"], _ = common.Marshal(apiError)
	sanitized, err := common.Marshal(event)
	if err != nil {
		return []byte(`{"type":"error","error":{"code":"service_unavailable","type":"server_error","message":"Request temporarily unavailable"}}`)
	}
	if ctx != nil && codexDispatchFailurePayload(sanitized) {
		value, _ := ctx.Get(newAPIPolicyRequestContextGinKey)
		if request, ok := value.(newAPIPolicyRequestContext); ok {
			if diagnostic, decodeErr := decodeCodexDispatchDiagnostic(encoded, request, time.Now()); decodeErr == nil {
				diagnostic.Stream = true
				request.DispatchAttempt.Record(diagnostic)
			}
		}
	}
	return sanitized
}
