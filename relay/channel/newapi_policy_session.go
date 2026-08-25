package channel

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	common2 "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const maxNewAPIPolicyEmbeddedMetadataBytes = 1 << 20

const (
	newAPIPolicyRootSessionResolved    = "resolved"
	newAPIPolicyRootSessionConflict    = "conflict"
	newAPIPolicyRootSessionUnavailable = "unavailable"

	newAPIPolicyRootSessionRelationRoot    = "root"
	newAPIPolicyRootSessionRelationRelated = "related"
)

type newAPIPolicyLabelEvidence struct {
	value    string
	conflict bool
}

func (e *newAPIPolicyLabelEvidence) add(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		e.conflict = true
		return
	}
	if e.value == "" {
		e.value = value
	} else if e.value != value {
		e.conflict = true
	}
}

func (e newAPIPolicyLabelEvidence) resolved() string {
	if e.conflict {
		return ""
	}
	return e.value
}

type newAPIPolicySessionEvidence struct {
	value    string
	sources  int
	conflict bool
}

func (e *newAPIPolicySessionEvidence) add(value string) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return
	}
	value = normalizeNewAPIPolicyRootSessionValue(raw)
	if value == "" {
		e.conflict = true
		return
	}
	if e.value == "" {
		e.value = value
	} else if e.value != value {
		e.conflict = true
	}
	e.sources++
}

type newAPIPolicySubagentEvidence struct {
	present      bool
	broken       bool
	value        string
	valuePrimary bool
}

func (e *newAPIPolicySubagentEvidence) add(value string, primary bool) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return
	}
	value = normalizeNewAPIPolicySubagentKind(raw)
	if value == "" {
		e.broken = true
		return
	}
	// X-OpenAI-Subagent and subagent_kind describe related but different
	// namespaces. Their exact labels may legitimately differ (for example,
	// guardian vs approval); root resolution only needs corroboration that this
	// is a child request.
	e.present = true
	if e.value == "" || (primary && !e.valuePrimary) {
		e.value = raw
		e.valuePrimary = primary
	}
}

type newAPIPolicyCodexTurnMetadata struct {
	SessionID                 string `json:"session_id"`
	ThreadID                  string `json:"thread_id"`
	ClientRequestID           string `json:"client_request_id"`
	CodexClientRequestID      string `json:"x-client-request-id"`
	UnderscoreClientRequestID string `json:"x_client_request_id"`
	WindowID                  string `json:"window_id"`
	CodexWindowID             string `json:"x-codex-window-id"`
	UnderscoreWindowID        string `json:"x_codex_window_id"`
	ParentThreadID            string `json:"parent_thread_id"`
	ForkedFromThreadID        string `json:"forked_from_thread_id"`
	CodexParentThreadID       string `json:"x-codex-parent-thread-id"`
	UnderscoreParentThreadID  string `json:"x_codex_parent_thread_id"`
	SubagentKind              string `json:"subagent_kind"`
	OpenAISubagent            string `json:"x-openai-subagent"`
	UnderscoreOpenAISubagent  string `json:"x_openai_subagent"`
	ThreadSource              string `json:"thread_source"`
	RequestKind               string `json:"request_kind"`
}

type newAPIPolicyCodexClientMetadata struct {
	SessionID                 string          `json:"session_id"`
	ThreadID                  string          `json:"thread_id"`
	ClientRequestID           string          `json:"client_request_id"`
	CodexClientRequestID      string          `json:"x-client-request-id"`
	UnderscoreClientRequestID string          `json:"x_client_request_id"`
	WindowID                  string          `json:"window_id"`
	CodexWindowID             string          `json:"x-codex-window-id"`
	UnderscoreWindowID        string          `json:"x_codex_window_id"`
	ParentThreadID            string          `json:"parent_thread_id"`
	ForkedFromThreadID        string          `json:"forked_from_thread_id"`
	CodexParentThreadID       string          `json:"x-codex-parent-thread-id"`
	UnderscoreParentThreadID  string          `json:"x_codex_parent_thread_id"`
	SubagentKind              string          `json:"subagent_kind"`
	OpenAISubagent            string          `json:"x-openai-subagent"`
	UnderscoreOpenAISubagent  string          `json:"x_openai_subagent"`
	CodexTurnMetadata         json.RawMessage `json:"x-codex-turn-metadata"`
	UnderscoreTurnMetadata    json.RawMessage `json:"x_codex_turn_metadata"`
	NestedClientMetadata      json.RawMessage `json:"client_metadata"`
	ThreadSource              string          `json:"thread_source"`
	RequestKind               string          `json:"request_kind"`
}

type newAPIPolicyRootSessionEvidence struct {
	headerSessions newAPIPolicySessionEvidence
	parents        newAPIPolicySessionEvidence
	forkedFrom     newAPIPolicySessionEvidence
	metadataRoots  newAPIPolicySessionEvidence
	threads        newAPIPolicySessionEvidence
	clientRequests newAPIPolicySessionEvidence
	windows        newAPIPolicySessionEvidence
	subagents      newAPIPolicySubagentEvidence
	threadSources  newAPIPolicyLabelEvidence
	requestKinds   newAPIPolicyLabelEvidence
	metadataBroken bool
}

type newAPIPolicyRootSessionResolution struct {
	rootID       string
	state        string
	relation     string
	threadSource string
	requestKind  string
	subagentKind string
}

// newAPIPolicyRootSessionID resolves the user-visible root conversation while
// preserving the legacy leaf identity separately. A child is collapsed only
// when an explicit parent or subagent marker proves that the differing leaf is
// part of a child request. Conflicting carriers omit the root identity entirely
// so a malformed request cannot merge two windows.
func newAPIPolicyRootSessionID(c *gin.Context, info *relaycommon.RelayInfo, currentSessionID string) (string, bool) {
	root, state := resolveNewAPIPolicyRootSessionID(c, info, currentSessionID)
	return root, state == newAPIPolicyRootSessionResolved
}

func resolveNewAPIPolicyRootSessionID(c *gin.Context, info *relaycommon.RelayInfo, currentSessionID string) (string, string) {
	resolution := analyzeNewAPIPolicyRootSession(c, info, currentSessionID)
	return resolution.rootID, resolution.state
}

func analyzeNewAPIPolicyRootSession(c *gin.Context, info *relaycommon.RelayInfo, currentSessionID string) newAPIPolicyRootSessionResolution {
	currentSessionID = normalizeNewAPIPolicyRootSessionValue(currentSessionID)
	evidence := collectNewAPIPolicyRootSessionEvidence(c, info)
	rootID, state := resolveNewAPIPolicyRootSessionEvidence(c, evidence, currentSessionID)
	relation := ""
	if state == newAPIPolicyRootSessionResolved {
		leafID, leafConflict := newAPIPolicyOneLeaf(evidence)
		if !leafConflict && (evidence.parents.value != "" || evidence.forkedFrom.value != "" || evidence.subagents.present || (leafID != "" && leafID != rootID)) {
			relation = newAPIPolicyRootSessionRelationRelated
		} else {
			relation = newAPIPolicyRootSessionRelationRoot
		}
	}
	return newAPIPolicyRootSessionResolution{
		rootID:       rootID,
		state:        state,
		relation:     relation,
		threadSource: evidence.threadSources.resolved(),
		requestKind:  evidence.requestKinds.resolved(),
		subagentKind: evidence.subagents.value,
	}
}

func collectNewAPIPolicyRootSessionEvidence(c *gin.Context, info *relaycommon.RelayInfo) newAPIPolicyRootSessionEvidence {
	evidence := newAPIPolicyRootSessionEvidence{}

	if c != nil && c.Request != nil {
		for _, name := range []string{"Session-Id", "Session_id"} {
			for _, value := range c.Request.Header.Values(name) {
				evidence.headerSessions.add(value)
			}
		}
		for _, value := range c.Request.Header.Values("X-Codex-Parent-Thread-Id") {
			evidence.parents.add(value)
		}
		for _, value := range c.Request.Header.Values("X-Codex-Forked-From-Thread-Id") {
			evidence.forkedFrom.add(value)
		}
		for _, value := range c.Request.Header.Values("X-OpenAI-Subagent") {
			evidence.subagents.add(value, false)
		}
		for _, value := range c.Request.Header.Values("Thread-Id") {
			evidence.threads.add(value)
		}
		for _, value := range c.Request.Header.Values("X-Client-Request-Id") {
			evidence.clientRequests.add(value)
		}
		for _, value := range c.Request.Header.Values("X-Codex-Window-Id") {
			evidence.addWindow(value)
		}
		for _, raw := range c.Request.Header.Values("X-Codex-Turn-Metadata") {
			evidence.addTurnMetadata([]byte(raw), strings.TrimSpace(raw) != "")
		}
	}

	if raw := newAPIPolicyClientMetadata(info); len(raw) > 0 {
		evidence.addClientMetadata(raw, true, 0)
	}
	return evidence
}

func resolveNewAPIPolicyRootSessionEvidence(c *gin.Context, evidence newAPIPolicyRootSessionEvidence, currentSessionID string) (string, string) {
	if evidence.conflict() {
		return "", newAPIPolicyRootSessionConflict
	}

	leafID, leafConflict := newAPIPolicyOneLeaf(evidence)
	if leafConflict {
		return "", newAPIPolicyRootSessionConflict
	}
	headerSession := evidence.headerSessions.value
	parentID := evidence.parents.value
	forkedFromID := evidence.forkedFrom.value
	metadataRoot := evidence.metadataRoots.value
	if metadataRoot != "" {
		// A main Codex task may expose its stable identity only inside turn or
		// client metadata. For a child, parent_thread_id names the immediate
		// parent and may be an intermediate subagent; it must never be promoted
		// to the user-visible root.
		if leafID == "" || !validNewAPIPolicyRootSessionUUID(metadataRoot) || !validNewAPIPolicyRootSessionUUID(leafID) || (parentID != "" && !validNewAPIPolicyRootSessionUUID(parentID)) || (forkedFromID != "" && !validNewAPIPolicyRootSessionUUID(forkedFromID)) {
			return "", newAPIPolicyRootSessionConflict
		}
		childGraph := leafID != metadataRoot
		corroboratedChild := parentID != "" || forkedFromID != "" || evidence.subagents.present
		if childGraph && !corroboratedChild {
			return "", newAPIPolicyRootSessionConflict
		}
		if headerSession != "" && headerSession != metadataRoot {
			// Some converted transports put the exact leaf in Session-Id. Accept
			// the metadata root only when the remaining graph proves that the
			// explicit ID is that same leaf.
			if !corroboratedChild || leafID == "" || headerSession != leafID {
				return "", newAPIPolicyRootSessionConflict
			}
		}
		return metadataRoot, newAPIPolicyRootSessionResolved
	}
	if headerSession != "" {
		// A legacy lone Session-Id is still a stable identity. Once native graph
		// fields appear, however, it is a proven root only when a leaf is present
		// and the root/leaf relation is coherent. Incomplete upgrade headers stay
		// unavailable so a response.create frame can supply the full graph.
		nativeGraph := leafID != "" || parentID != "" || forkedFromID != "" || evidence.subagents.present
		if !nativeGraph && c != nil && c.Request != nil && websocket.IsWebSocketUpgrade(c.Request) {
			return "", newAPIPolicyRootSessionUnavailable
		}
		if nativeGraph {
			if leafID == "" {
				return "", newAPIPolicyRootSessionUnavailable
			}
			if !validNewAPIPolicyRootSessionUUID(headerSession) || !validNewAPIPolicyRootSessionUUID(leafID) || (parentID != "" && !validNewAPIPolicyRootSessionUUID(parentID)) || (forkedFromID != "" && !validNewAPIPolicyRootSessionUUID(forkedFromID)) {
				return "", newAPIPolicyRootSessionConflict
			}
			if headerSession != leafID && parentID == "" && forkedFromID == "" && !evidence.subagents.present {
				return "", newAPIPolicyRootSessionConflict
			}
		}
		return headerSession, newAPIPolicyRootSessionResolved
	}
	// Once any native graph carrier is present, a higher-priority legacy
	// Conversation-Id must not be mistaken for the Codex root. The exact legacy
	// identity remains available separately as SessionFingerprint.
	if leafID != "" || parentID != "" || forkedFromID != "" || evidence.subagents.present {
		return "", newAPIPolicyRootSessionUnavailable
	}
	if currentSessionID == "" {
		return "", newAPIPolicyRootSessionUnavailable
	}
	if c != nil && c.Request != nil && websocket.IsWebSocketUpgrade(c.Request) {
		return "", newAPIPolicyRootSessionUnavailable
	}
	return currentSessionID, newAPIPolicyRootSessionResolved
}

func (e *newAPIPolicyRootSessionEvidence) conflict() bool {
	return e == nil || e.metadataBroken || e.subagents.broken || e.headerSessions.conflict || e.parents.conflict || e.forkedFrom.conflict || e.metadataRoots.conflict || e.threads.conflict || e.clientRequests.conflict || e.windows.conflict
}

func (e *newAPIPolicyRootSessionEvidence) addTurnMetadata(raw []byte, present bool) {
	if e == nil || !present {
		return
	}
	if bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return
	}
	var metadata newAPIPolicyCodexTurnMetadata
	if !decodeNewAPIPolicyEmbeddedObject(raw, &metadata) {
		e.metadataBroken = true
		return
	}
	e.metadataRoots.add(metadata.SessionID)
	e.threads.add(metadata.ThreadID)
	e.clientRequests.add(metadata.ClientRequestID)
	e.clientRequests.add(metadata.CodexClientRequestID)
	e.clientRequests.add(metadata.UnderscoreClientRequestID)
	e.addWindow(metadata.WindowID)
	e.addWindow(metadata.CodexWindowID)
	e.addWindow(metadata.UnderscoreWindowID)
	e.parents.add(metadata.ParentThreadID)
	e.forkedFrom.add(metadata.ForkedFromThreadID)
	e.parents.add(metadata.CodexParentThreadID)
	e.parents.add(metadata.UnderscoreParentThreadID)
	e.subagents.add(metadata.SubagentKind, true)
	e.subagents.add(metadata.OpenAISubagent, false)
	e.subagents.add(metadata.UnderscoreOpenAISubagent, false)
	e.threadSources.add(metadata.ThreadSource)
	e.requestKinds.add(metadata.RequestKind)
}

func (e *newAPIPolicyRootSessionEvidence) addClientMetadata(raw []byte, present bool, depth int) {
	if e == nil || !present {
		return
	}
	if depth > 3 {
		e.metadataBroken = true
		return
	}
	if bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return
	}
	var metadata newAPIPolicyCodexClientMetadata
	if !decodeNewAPIPolicyEmbeddedObject(raw, &metadata) {
		e.metadataBroken = true
		return
	}
	e.metadataRoots.add(metadata.SessionID)
	e.threads.add(metadata.ThreadID)
	e.clientRequests.add(metadata.ClientRequestID)
	e.clientRequests.add(metadata.CodexClientRequestID)
	e.clientRequests.add(metadata.UnderscoreClientRequestID)
	e.addWindow(metadata.WindowID)
	e.addWindow(metadata.CodexWindowID)
	e.addWindow(metadata.UnderscoreWindowID)
	e.parents.add(metadata.ParentThreadID)
	e.forkedFrom.add(metadata.ForkedFromThreadID)
	e.parents.add(metadata.CodexParentThreadID)
	e.parents.add(metadata.UnderscoreParentThreadID)
	e.subagents.add(metadata.SubagentKind, true)
	e.subagents.add(metadata.OpenAISubagent, false)
	e.subagents.add(metadata.UnderscoreOpenAISubagent, false)
	e.threadSources.add(metadata.ThreadSource)
	e.requestKinds.add(metadata.RequestKind)
	if len(bytes.TrimSpace(metadata.CodexTurnMetadata)) > 0 && string(bytes.TrimSpace(metadata.CodexTurnMetadata)) != "null" {
		e.addTurnMetadata(metadata.CodexTurnMetadata, true)
	}
	if len(bytes.TrimSpace(metadata.UnderscoreTurnMetadata)) > 0 && string(bytes.TrimSpace(metadata.UnderscoreTurnMetadata)) != "null" {
		e.addTurnMetadata(metadata.UnderscoreTurnMetadata, true)
	}
	if len(bytes.TrimSpace(metadata.NestedClientMetadata)) > 0 && string(bytes.TrimSpace(metadata.NestedClientMetadata)) != "null" {
		e.addClientMetadata(metadata.NestedClientMetadata, true, depth+1)
	}
}

func newAPIPolicyClientMetadata(info *relaycommon.RelayInfo) []byte {
	if info == nil {
		return nil
	}
	switch request := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		if request != nil {
			return request.ClientMetadata
		}
	}
	return nil
}

func decodeNewAPIPolicyEmbeddedObject(raw []byte, target any) bool {
	raw = bytes.TrimSpace(raw)
	for depth := 0; depth <= 4; depth++ {
		if len(raw) == 0 || len(raw) > maxNewAPIPolicyEmbeddedMetadataBytes {
			return false
		}
		if raw[0] != '"' {
			return raw[0] == '{' && common2.Unmarshal(raw, target) == nil
		}
		if depth == 4 {
			return false
		}
		var nested string
		if common2.Unmarshal(raw, &nested) != nil {
			return false
		}
		raw = bytes.TrimSpace([]byte(nested))
	}
	return false
}

func validNewAPIPolicyRootSessionUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func normalizeNewAPIPolicySubagentKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func normalizeNewAPIPolicyRootSessionValue(value string) string {
	value = normalizeNewAPIPolicySessionID(value)
	if value == "" || strings.ContainsRune(value, '\x00') {
		return ""
	}
	if parsed, err := uuid.Parse(value); err == nil {
		return strings.ToLower(parsed.String())
	}
	return value
}

func (e *newAPIPolicyRootSessionEvidence) addWindow(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	separator := strings.LastIndexByte(value, ':')
	if separator <= 0 || separator == len(value)-1 {
		e.metadataBroken = true
		return
	}
	thread := normalizeNewAPIPolicyRootSessionValue(value[:separator])
	if thread == "" {
		e.metadataBroken = true
		return
	}
	if _, err := strconv.ParseUint(strings.TrimSpace(value[separator+1:]), 10, 64); err != nil {
		e.metadataBroken = true
		return
	}
	e.windows.add(thread)
}

func newAPIPolicyOneLeaf(evidence newAPIPolicyRootSessionEvidence) (string, bool) {
	leaf := ""
	for _, candidate := range []string{evidence.threads.value, evidence.clientRequests.value, evidence.windows.value} {
		if candidate == "" {
			continue
		}
		if leaf == "" {
			leaf = candidate
			continue
		}
		if leaf != candidate {
			return "", true
		}
	}
	return leaf, false
}
