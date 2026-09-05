package channel

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const newAPIPolicyDecisionSignatureVersion = "v1"
const newAPIPolicyStreamLineLimit = 256 << 10

type newAPIPolicyDecision struct {
	RequestID      string
	DecisionID     string
	Action         string
	Profile        string
	ReasonCode     string
	Severity       string
	StrikeEligible bool
	RuleVersion    string
	EvidenceSHA256 string
	Signature      string
}

type newAPIPolicyWebSocketEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Code    string                       `json:"code"`
		Details newAPIPolicyWebSocketDetails `json:"details"`
	} `json:"error"`
}

type newAPIPolicyWebSocketDetails struct {
	RequestID         string `json:"request_id"`
	DecisionID        string `json:"decision_id"`
	EventID           string `json:"event_id"`
	Action            string `json:"action"`
	Profile           string `json:"profile"`
	ReasonCode        string `json:"reason_code"`
	Severity          string `json:"severity"`
	StrikeEligible    bool   `json:"strike_eligible"`
	RuleVersion       string `json:"rule_version"`
	EvidenceSHA256    string `json:"evidence_sha256"`
	SignatureVersion  string `json:"signature_version"`
	ResponseSignature string `json:"response_signature"`
	EventVersion      string `json:"event_signature_version"`
	EventSignature    string `json:"event_signature"`
}

type NewAPIPolicyWebSocketResult struct {
	Verified  bool
	Terminate bool
}

type newAPIPolicyStreamCarrier struct {
	Error    newAPIPolicyStreamError `json:"error"`
	Response struct {
		Error newAPIPolicyStreamError `json:"error"`
	} `json:"response"`
}

type newAPIPolicyStreamError struct {
	Details struct {
		Policy newAPIPolicyWebSocketDetails `json:"codex2api_policy"`
	} `json:"details"`
}

type newAPIPolicyStreamBody struct {
	io.ReadCloser
	c              *gin.Context
	resp           *http.Response
	requestContext newAPIPolicyRequestContext
	line           []byte
	discardLine    bool
	finished       bool
	seen           map[string]struct{}
}

func (b *newAPIPolicyStreamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.observe(p[:n])
	}
	if err == io.EOF && !b.finished {
		b.finished = true
		if !b.discardLine && len(b.line) > 0 {
			b.processLine(b.line)
		}
		b.line = nil
	}
	return n, err
}

func (b *newAPIPolicyStreamBody) observe(chunk []byte) {
	for len(chunk) > 0 {
		newline := bytes.IndexByte(chunk, '\n')
		if newline < 0 {
			b.appendLine(chunk)
			return
		}
		b.appendLine(chunk[:newline])
		if !b.discardLine {
			b.processLine(b.line)
		}
		b.line = b.line[:0]
		b.discardLine = false
		chunk = chunk[newline+1:]
	}
}

func (b *newAPIPolicyStreamBody) appendLine(fragment []byte) {
	if b.discardLine {
		return
	}
	if len(b.line)+len(fragment) > newAPIPolicyStreamLineLimit {
		b.line = b.line[:0]
		b.discardLine = true
		return
	}
	b.line = append(b.line, fragment...)
}

func (b *newAPIPolicyStreamBody) processLine(line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	data := bytes.TrimSpace(line[len("data:"):])
	if !bytes.Contains(data, []byte(`"codex2api_policy"`)) {
		return
	}
	var carrier newAPIPolicyStreamCarrier
	if err := common.Unmarshal(data, &carrier); err != nil {
		logger.LogWarn(b.c, "ignored malformed Codex2API stream policy decision")
		return
	}
	details := carrier.Error.Details.Policy
	if details.DecisionID == "" {
		details = carrier.Response.Error.Details.Policy
	}
	if details.DecisionID == "" {
		return
	}
	if _, duplicate := b.seen[details.DecisionID]; duplicate {
		return
	}
	decision, err := verifyNewAPIPolicyDecisionDetails(details, b.requestContext)
	if err != nil {
		logger.LogWarn(b.c, fmt.Sprintf("ignored invalid Codex2API stream policy decision: %s", err.Error()))
		return
	}
	b.seen[details.DecisionID] = struct{}{}
	service.MarkCodex2APIPolicyViolation(b.resp)
	applyVerifiedNewAPIPolicyDecision(b.c, decision, b.requestContext)
}

func processNewAPIPolicyResponse(c *gin.Context, resp *http.Response) bool {
	if resp == nil {
		return false
	}
	var requestContext newAPIPolicyRequestContext
	if resp.Request != nil {
		requestContext, _ = resp.Request.Context().Value(newAPIPolicyRequestContextKey{}).(newAPIPolicyRequestContext)
	}
	processed := processNewAPIPolicyResponseWithContext(c, resp, requestContext)
	if resp.Body != nil && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		resp.Body = &codexDispatchStreamBody{ReadCloser: resp.Body, reader: bufio.NewReaderSize(resp.Body, 4096), request: requestContext}
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		if !processed && requestContext.Secret != "" {
			resp.Body = &newAPIPolicyStreamBody{ReadCloser: resp.Body, c: c, resp: resp, requestContext: requestContext, seen: make(map[string]struct{})}
		}
	}
	return processed
}

func processNewAPIPolicyResponseWithContext(c *gin.Context, resp *http.Response, requestContext newAPIPolicyRequestContext) bool {
	processCodexDispatchHeader(resp, requestContext)
	if requestContext.Secret == "" || requestContext.RequestID == "" {
		return false
	}
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Header.Get("X-Codex2API-Policy-Violation")), "true") {
		return false
	}
	decision, err := verifyNewAPIPolicyDecision(resp.Header, requestContext)
	if err != nil {
		logger.LogWarn(c, fmt.Sprintf("ignored invalid Codex2API policy decision: %s", err.Error()))
		return false
	}

	// Mark before persistence. A verified provider-side policy block must never
	// be retried through another channel even if local audit storage is down.
	service.MarkCodex2APIPolicyViolation(resp)
	applyResult := applyVerifiedNewAPIPolicyDecision(c, decision, requestContext)
	return applyResult.Verified
}

func applyVerifiedNewAPIPolicyDecision(c *gin.Context, decision newAPIPolicyDecision, requestContext newAPIPolicyRequestContext) NewAPIPolicyWebSocketResult {
	enforcement := requestContext.Enforcement
	if !enforcement.AuditEnabled && !enforcement.StrikeEnabled {
		return NewAPIPolicyWebSocketResult{Verified: true}
	}

	result, err := model.ApplyCodexPolicyDecision(model.CodexPolicyDecisionInput{
		Decision: model.CodexPolicyDecision{
			DecisionID: decision.DecisionID, RequestID: decision.RequestID,
			UserID: requestContext.UserID, ClientIP: requestContext.ClientIP,
			PlatformID: requestContext.PlatformID, ChannelID: requestContext.ChannelID,
			Action: decision.Action, Profile: decision.Profile, ReasonCode: decision.ReasonCode,
			Severity: decision.Severity, StrikeEligible: decision.StrikeEligible,
			RuleVersion: decision.RuleVersion, EvidenceSHA256: decision.EvidenceSHA256,
			SignatureVersion: newAPIPolicyDecisionSignatureVersion, CreatedAt: time.Now().Unix(),
		},
		AuditEnabled: enforcement.AuditEnabled, StrikeEnabled: enforcement.StrikeEnabled,
		AccountBanEnabled: enforcement.AccountBanEnabled, IPBlockEnabled: enforcement.IPBlockEnabled,
		BanAfter: enforcement.BanAfter, WindowSeconds: enforcement.WindowSeconds,
	})
	if err != nil {
		logger.LogError(c, "failed to persist verified Codex2API policy decision: "+err.Error())
		return NewAPIPolicyWebSocketResult{Verified: true}
	}
	logger.LogWarn(c, fmt.Sprintf("verified Codex2API policy decision decision_id=%s reason=%s strike_count=%d duplicate=%t protected=%t account_banned=%t ip_blocked=%t",
		decision.DecisionID, decision.ReasonCode, result.StrikeCount, result.Duplicate, result.Protected, result.AccountBanned, result.IPBlocked))
	return NewAPIPolicyWebSocketResult{
		Verified:  true,
		Terminate: result.AccountBanned || result.IPBlocked,
	}
}

// ProcessNewAPIPolicyWebSocketMessage verifies and applies a signed per-turn
// Codex2API policy decision carried in an OpenAI Responses WebSocket error.
func ProcessNewAPIPolicyWebSocketMessage(c *gin.Context, message []byte) NewAPIPolicyWebSocketResult {
	if c == nil || !bytes.Contains(message, []byte(`"request_policy_violation"`)) {
		return NewAPIPolicyWebSocketResult{}
	}
	value, ok := c.Get(newAPIPolicyRequestContextGinKey)
	if !ok {
		return NewAPIPolicyWebSocketResult{}
	}
	requestContext, ok := value.(newAPIPolicyRequestContext)
	if !ok {
		return NewAPIPolicyWebSocketResult{}
	}
	var envelope newAPIPolicyWebSocketEnvelope
	if err := common.Unmarshal(message, &envelope); err != nil || envelope.Type != "error" || envelope.Error.Code != "request_policy_violation" {
		return NewAPIPolicyWebSocketResult{}
	}
	details := envelope.Error.Details
	decision, err := verifyNewAPIPolicyDecisionDetails(details, requestContext)
	if err != nil {
		logger.LogWarn(c, fmt.Sprintf("ignored invalid Codex2API WebSocket policy decision: %s", err.Error()))
		return NewAPIPolicyWebSocketResult{}
	}
	if details.EventVersion != newAPIPolicyDecisionSignatureVersion || !validNewAPIPolicyToken(details.EventID, 128) || !validLowerHex(strings.ToLower(strings.TrimSpace(details.EventSignature)), 64, 64) {
		logger.LogWarn(c, "ignored invalid Codex2API WebSocket policy event identity")
		return NewAPIPolicyWebSocketResult{}
	}
	eventCanonical := strings.Join([]string{
		"policy-event-v1", decision.RequestID, decision.DecisionID, details.EventID,
		decision.Action, decision.Profile, decision.ReasonCode, decision.Severity,
		strconv.FormatBool(decision.StrikeEligible), decision.RuleVersion, decision.EvidenceSHA256,
	}, "\n")
	if !secureHexEqual(newAPIHMAC(requestContext.Secret, eventCanonical), strings.ToLower(strings.TrimSpace(details.EventSignature))) {
		logger.LogWarn(c, "ignored Codex2API WebSocket policy event with invalid signature")
		return NewAPIPolicyWebSocketResult{}
	}
	return applyVerifiedNewAPIPolicyDecision(c, decision, requestContext)
}

func verifyNewAPIPolicyDecisionDetails(details newAPIPolicyWebSocketDetails, requestContext newAPIPolicyRequestContext) (newAPIPolicyDecision, error) {
	header := make(http.Header)
	header.Set("X-Codex2API-Policy-Request-ID", details.RequestID)
	header.Set("X-Codex2API-Policy-Decision-ID", details.DecisionID)
	header.Set("X-Codex2API-Policy-Action", details.Action)
	header.Set("X-Codex2API-Policy-Profile", details.Profile)
	header.Set("X-Codex2API-Policy-Reason", details.ReasonCode)
	header.Set("X-Codex2API-Policy-Severity", details.Severity)
	header.Set("X-Codex2API-Policy-Strike-Eligible", strconv.FormatBool(details.StrikeEligible))
	header.Set("X-Codex2API-Policy-Rule-Version", details.RuleVersion)
	header.Set("X-Codex2API-Policy-Evidence-SHA256", details.EvidenceSHA256)
	header.Set("X-Codex2API-Policy-Signature-Version", details.SignatureVersion)
	header.Set("X-Codex2API-Policy-Response-Signature", details.ResponseSignature)
	return verifyNewAPIPolicyDecision(header, requestContext)
}

func verifyNewAPIPolicyDecision(header http.Header, requestContext newAPIPolicyRequestContext) (newAPIPolicyDecision, error) {
	decision := newAPIPolicyDecision{
		RequestID:      strings.TrimSpace(header.Get("X-Codex2API-Policy-Request-ID")),
		DecisionID:     strings.TrimSpace(header.Get("X-Codex2API-Policy-Decision-ID")),
		Action:         strings.ToLower(strings.TrimSpace(header.Get("X-Codex2API-Policy-Action"))),
		Profile:        strings.ToLower(strings.TrimSpace(header.Get("X-Codex2API-Policy-Profile"))),
		ReasonCode:     strings.TrimSpace(header.Get("X-Codex2API-Policy-Reason")),
		Severity:       strings.ToLower(strings.TrimSpace(header.Get("X-Codex2API-Policy-Severity"))),
		RuleVersion:    strings.ToLower(strings.TrimSpace(header.Get("X-Codex2API-Policy-Rule-Version"))),
		EvidenceSHA256: strings.ToLower(strings.TrimSpace(header.Get("X-Codex2API-Policy-Evidence-SHA256"))),
		Signature:      strings.ToLower(strings.TrimSpace(header.Get("X-Codex2API-Policy-Response-Signature"))),
	}
	if strings.TrimSpace(header.Get("X-Codex2API-Policy-Signature-Version")) != newAPIPolicyDecisionSignatureVersion {
		return newAPIPolicyDecision{}, fmt.Errorf("unsupported decision signature version")
	}
	if decision.RequestID == "" || decision.RequestID != requestContext.RequestID {
		return newAPIPolicyDecision{}, fmt.Errorf("decision request id does not match the signed request")
	}
	if !validNewAPIPolicyToken(decision.DecisionID, 128) || !validNewAPIPolicyToken(decision.ReasonCode, 128) {
		return newAPIPolicyDecision{}, fmt.Errorf("invalid decision identity or reason")
	}
	switch decision.Action {
	case "block", "warn":
	default:
		return newAPIPolicyDecision{}, fmt.Errorf("invalid policy action")
	}
	switch decision.Profile {
	case "balanced", "strict", "research":
	default:
		return newAPIPolicyDecision{}, fmt.Errorf("invalid policy profile")
	}
	switch decision.Severity {
	case "low", "medium", "high", "critical":
	default:
		return newAPIPolicyDecision{}, fmt.Errorf("invalid policy severity")
	}
	strikeEligible, err := strconv.ParseBool(strings.TrimSpace(header.Get("X-Codex2API-Policy-Strike-Eligible")))
	if err != nil {
		return newAPIPolicyDecision{}, fmt.Errorf("invalid strike eligibility")
	}
	decision.StrikeEligible = strikeEligible
	if decision.StrikeEligible && decision.Action != "block" {
		return newAPIPolicyDecision{}, fmt.Errorf("only block decisions may be strike eligible")
	}
	if !validLowerHex(decision.RuleVersion, 8, 64) || !validLowerHex(decision.EvidenceSHA256, 64, 64) || !validLowerHex(decision.Signature, 64, 64) {
		return newAPIPolicyDecision{}, fmt.Errorf("invalid policy digest or signature")
	}
	canonical := strings.Join([]string{
		"policy-decision-v1", decision.RequestID, decision.DecisionID,
		decision.Action, decision.Profile, decision.ReasonCode, decision.Severity,
		strconv.FormatBool(decision.StrikeEligible), decision.RuleVersion, decision.EvidenceSHA256,
	}, "\n")
	expected := newAPIHMAC(requestContext.Secret, canonical)
	if !secureHexEqual(expected, decision.Signature) {
		return newAPIPolicyDecision{}, fmt.Errorf("policy decision signature mismatch")
	}
	return decision, nil
}

func validNewAPIPolicyToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}

func validLowerHex(value string, minimum int, maximum int) bool {
	if len(value) < minimum || len(value) > maximum || len(value)%2 != 0 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == len(value)
}

func secureHexEqual(left string, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && hmac.Equal(leftBytes, rightBytes)
}
