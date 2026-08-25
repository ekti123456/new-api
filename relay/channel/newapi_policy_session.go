package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	common2 "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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

// CodexRootSessionResolution is the distribution-safe subset of the native
// Codex graph analysis. Related is true only when a coherent root/leaf graph,
// parent/fork carrier, or subagent marker proves that this is a derived request;
// thread_source alone is never sufficient.
type CodexRootSessionResolution struct {
	RootID       string
	Resolved     bool
	Related      bool
	ThreadSource string
	RequestKind  string
	SubagentKind string
}

const codexPassiveRootSessionOverrideContextKey = "newapi_codex_passive_root_session_override_v1"

type codexPassiveRootSessionOverride struct {
	rootID  string
	feature string
}

// ResolveCodexRootSessionForDistribution resolves the same native graph used
// by signed policy metadata before channel selection. Parsing the reusable body
// preserves it for the relay handler that follows.
func ResolveCodexRootSessionForDistribution(c *gin.Context) CodexRootSessionResolution {
	var request dto.OpenAIResponsesRequest
	info := &relaycommon.RelayInfo{}
	if c != nil {
		if err := common2.UnmarshalBodyReusable(c, &request); err == nil {
			info.Request = &request
		}
	}
	stableSessionID := newAPIPolicyStableSessionID(c, info)
	resolution := applyCodexPassiveRootSessionOverride(c, analyzeNewAPIPolicyRootSession(c, info, stableSessionID))
	return CodexRootSessionResolution{
		RootID:       resolution.rootID,
		Resolved:     resolution.state == newAPIPolicyRootSessionResolved,
		Related:      resolution.relation == newAPIPolicyRootSessionRelationRelated,
		ThreadSource: resolution.threadSource,
		RequestKind:  resolution.requestKind,
		SubagentKind: resolution.subagentKind,
	}
}

// SetCodexPassiveRootSessionOverride associates a tightly classified Codex
// metadata generation with the recent root request selected by NewAPI. The
// override is applied again when signed policy metadata is created, so
// Codex2API receives the same verified root fingerprint and can keep the
// passive generation on the root account.
func SetCodexPassiveRootSessionOverride(c *gin.Context, rootID, feature string) bool {
	rootID = normalizeNewAPIPolicyRootSessionValue(rootID)
	feature = strings.TrimSpace(feature)
	if c == nil || rootID == "" || feature == "" {
		return false
	}
	c.Set(codexPassiveRootSessionOverrideContextKey, codexPassiveRootSessionOverride{rootID: rootID, feature: feature})
	return true
}

func applyCodexPassiveRootSessionOverride(c *gin.Context, resolution newAPIPolicyRootSessionResolution) newAPIPolicyRootSessionResolution {
	if c == nil {
		return resolution
	}
	raw, found := c.Get(codexPassiveRootSessionOverrideContextKey)
	override, ok := raw.(codexPassiveRootSessionOverride)
	if !found || !ok || override.rootID == "" || override.feature == "" {
		return resolution
	}
	resolution.rootID = override.rootID
	resolution.state = newAPIPolicyRootSessionResolved
	resolution.relation = newAPIPolicyRootSessionRelationRelated
	if override.feature == "guardian_approval" {
		if resolution.threadSource == "" {
			resolution.threadSource = "subagent"
		}
		if resolution.requestKind == "" {
			resolution.requestKind = "turn"
		}
		if resolution.subagentKind == "" {
			resolution.subagentKind = "guardian"
		}
	}
	return resolution
}

// ClassifyCodexGuardianApproval recognizes the hidden approval-review request
// emitted by Codex. Unlike ordinary child turns, current desktop builds put
// the authoritative parent task in a fixed input line and may omit the native
// parent graph from transport headers. The full fixed prompt envelope is
// required so a direct Luna request containing an arbitrary UUID cannot use
// this route.
func ClassifyCodexGuardianApproval(c *gin.Context, modelName string) (string, bool) {
	if c == nil {
		return "", false
	}
	var request dto.OpenAIResponsesRequest
	if err := common2.UnmarshalBodyReusable(c, &request); err != nil {
		return "", false
	}
	requestedModel := normalizeCodexInternalModelName(request.Model)
	if requestedModel == "" || requestedModel != normalizeCodexInternalModelName(modelName) ||
		(requestedModel != "gpt-5.6-luna" && requestedModel != "codex-auto-review") ||
		request.Reasoning == nil || !strings.EqualFold(strings.TrimSpace(request.Reasoning.Effort), "low") {
		return "", false
	}

	var instructions string
	if common2.Unmarshal(request.Instructions, &instructions) != nil {
		return "", false
	}
	instructions = strings.ReplaceAll(strings.TrimSpace(instructions), "\r\n", "\n")
	if !strings.HasPrefix(instructions, "You are judging one planned coding-agent action.\nAssess the exact action's intrinsic risk and whether the transcript authorizes its target and side effects.") {
		return "", false
	}

	parts := request.ParseInput()
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "input_text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	inputText := strings.ReplaceAll(strings.Join(texts, "\n"), "\r\n", "\n")
	transcriptStart := strings.Index(inputText, ">>> TRANSCRIPT START")
	transcriptEnd := strings.LastIndex(inputText, ">>> TRANSCRIPT END")
	approvalStart := strings.LastIndex(inputText, ">>> APPROVAL REQUEST START")
	approvalEnd := strings.LastIndex(inputText, ">>> APPROVAL REQUEST END")
	if !strings.HasPrefix(strings.TrimSpace(inputText), "The following is the Codex agent history whose request action you are assessing.") ||
		transcriptStart < 0 || transcriptEnd <= transcriptStart || approvalStart <= transcriptEnd || approvalEnd <= approvalStart ||
		!strings.Contains(inputText[transcriptEnd:approvalStart], "The Codex agent has requested the following action:") ||
		!strings.Contains(inputText[approvalStart:approvalEnd], "Planned action JSON:") {
		return "", false
	}

	const reviewedMarker = "Reviewed Codex session id:"
	bridge := inputText[transcriptEnd+len(">>> TRANSCRIPT END") : approvalStart]
	if strings.Count(bridge, reviewedMarker) != 1 {
		return "", false
	}
	reviewed := bridge[strings.Index(bridge, reviewedMarker)+len(reviewedMarker):]
	if newline := strings.IndexByte(reviewed, '\n'); newline >= 0 {
		reviewed = reviewed[:newline]
	}
	fields := strings.Fields(reviewed)
	if len(fields) != 1 {
		return "", false
	}
	rootID := normalizeNewAPIPolicyRootSessionValue(fields[0])
	if !validNewAPIPolicyRootSessionUUID(rootID) {
		return "", false
	}
	return rootID, true
}

// ClassifyUnlinkedCodexPassiveInternalRequest recognizes only Codex's fixed,
// structured system generations that currently lack a parent thread ID. A
// model name or thread_source label alone is never sufficient: the request
// must also use low reasoning, a known prompt, and the matching constrained
// output schema. This keeps arbitrary direct Luna prompts on the normal model
// authorization path.
func ClassifyUnlinkedCodexPassiveInternalRequest(c *gin.Context, resolution CodexRootSessionResolution, modelName string) (string, bool) {
	feature, _, ok := ClassifyUnlinkedCodexPassiveInternalRequestWithCorrelation(c, resolution, modelName)
	return feature, ok
}

// ClassifyUnlinkedCodexPassiveInternalRequestWithCorrelation additionally
// returns a prompt-derived key for the first-title generation. The key lets
// the distributor distinguish two new Codex tasks created concurrently by
// the same user and API token instead of relying on a last-request-wins slot.
func ClassifyUnlinkedCodexPassiveInternalRequestWithCorrelation(c *gin.Context, resolution CodexRootSessionResolution, modelName string) (string, string, bool) {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	modelName = strings.TrimSuffix(modelName, ratio_setting.CompactModelSuffix)
	if c == nil || modelName != "gpt-5.6-luna" || !resolution.Resolved || resolution.Related || resolution.RootID == "" {
		return "", "", false
	}

	var request dto.OpenAIResponsesRequest
	if err := common2.UnmarshalBodyReusable(c, &request); err != nil {
		return "", "", false
	}
	requestModel := strings.ToLower(strings.TrimSpace(request.Model))
	requestModel = strings.TrimSuffix(requestModel, ratio_setting.CompactModelSuffix)
	if requestModel != "gpt-5.6-luna" || request.Reasoning == nil || !strings.EqualFold(strings.TrimSpace(request.Reasoning.Effort), "low") {
		return "", "", false
	}

	inputs := request.ParseInput()
	firstText := ""
	for _, input := range inputs {
		if input.Type == "input_text" && strings.TrimSpace(input.Text) != "" {
			firstText = strings.TrimSpace(input.Text)
			break
		}
	}
	if firstText == "" {
		return "", "", false
	}

	switch strings.ToLower(strings.TrimSpace(resolution.ThreadSource)) {
	case "system":
		if len(request.GetToolsMap()) != 0 {
			return "", "", false
		}
		if strings.HasPrefix(firstText, "You are a helpful assistant. You will be presented with a user prompt, and your job is to provide a short title for a task that will be created from that prompt.") &&
			hasExactStructuredProperties(request.Text, "title", "description") {
			correlationKey := codexTitlePromptCorrelationKey(firstText)
			if correlationKey == "" {
				return "", "", false
			}
			return "thread_title", correlationKey, true
		}
		if strings.HasPrefix(firstText, "You write the one-line activity update displayed beneath an existing Codex task title.") &&
			(hasExactStructuredProperties(request.Text, "summary") || hasExactStructuredProperties(request.Text, "summary", "compactSummary")) {
			return "thread_summary", "", true
		}
	}
	return "", "", false
}

// ClassifyCodexSessionAccountingBypass recognizes Codex Desktop's independent
// ambient-suggestion jobs. These project-level background threads have no
// trustworthy parent conversation, so they must use ordinary scheduling and
// their own affinity identity rather than borrowing the most recent root.
// The result is carried only in signed policy metadata and affects window
// accounting, not concurrency, logging, billing, or model authorization.
func ClassifyCodexSessionAccountingBypass(c *gin.Context, resolution CodexRootSessionResolution, modelName string) (string, bool) {
	if c == nil || !resolution.Resolved || resolution.Related || strings.TrimSpace(resolution.RootID) == "" ||
		!strings.EqualFold(strings.TrimSpace(resolution.ThreadSource), "system") {
		return "", false
	}

	var request dto.OpenAIResponsesRequest
	if err := common2.UnmarshalBodyReusable(c, &request); err != nil {
		return "", false
	}
	requestedModel := normalizeCodexInternalModelName(request.Model)
	if requestedModel == "" || requestedModel != normalizeCodexInternalModelName(modelName) || len(request.GetToolsMap()) != 0 {
		return "", false
	}

	if requestedModel == "gpt-5.4-mini" &&
		strings.Contains(strings.TrimSpace(string(request.Instructions)), "Classify Codex ambient suggestion candidates for policy safety.") &&
		hasExactStructuredProperties(request.Text, "exclude") {
		return "ambient_suggestion_safety", true
	}

	if requestedModel != "gpt-5.6-terra" && requestedModel != "gpt-5.4" {
		return "", false
	}
	if request.Reasoning == nil || !strings.EqualFold(strings.TrimSpace(request.Reasoning.Effort), "medium") {
		return "", false
	}
	firstText := ""
	for _, input := range request.ParseInput() {
		if input.Type == "input_text" && strings.TrimSpace(input.Text) != "" {
			firstText = strings.TrimSpace(strings.ReplaceAll(input.Text, "\r\n", "\n"))
			break
		}
	}
	if !strings.HasPrefix(firstText, "# Overview\n\nGenerate 0 to 3 hyperpersonalized suggestions for what this user can do with Codex") ||
		!hasExactStructuredProperties(request.Text, "suggestions") {
		return "", false
	}
	return "ambient_suggestions", true
}

func normalizeCodexInternalModelName(modelName string) string {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	return strings.TrimSuffix(modelName, ratio_setting.CompactModelSuffix)
}

// CodexRootPromptCorrelationKey extracts the initial user prompt from a
// native Responses request and mirrors the first-title generator's 2,000
// character prefix. It is observational only and never forwarded upstream.
func CodexRootPromptCorrelationKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	var request dto.OpenAIResponsesRequest
	if err := common2.UnmarshalBodyReusable(c, &request); err != nil {
		return ""
	}
	prompt := codexLatestUserInputText(request.Input)
	return codexPromptCorrelationKey(prompt)
}

func codexTitlePromptCorrelationKey(titlePrompt string) string {
	const marker = "\nUser prompt:\n"
	index := strings.LastIndex(titlePrompt, marker)
	if index < 0 {
		return ""
	}
	return codexPromptCorrelationKey(titlePrompt[index+len(marker):])
}

func codexPromptCorrelationKey(prompt string) string {
	prompt = strings.TrimSpace(strings.ReplaceAll(prompt, "\r\n", "\n"))
	if prompt == "" {
		return ""
	}
	runes := []rune(prompt)
	if len(runes) > 2000 {
		prompt = string(runes[:2000])
	}
	digest := sha256.Sum256([]byte("codex-thread-title-v1\x00" + prompt))
	return hex.EncodeToString(digest[:])
}

func codexLatestUserInputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var direct string
	if err := common2.Unmarshal(raw, &direct); err == nil {
		return direct
	}
	var inputs []dto.Input
	if err := common2.Unmarshal(raw, &inputs); err != nil {
		return ""
	}
	for index := len(inputs) - 1; index >= 0; index-- {
		if !strings.EqualFold(strings.TrimSpace(inputs[index].Role), "user") {
			continue
		}
		if text := codexInputContentText(inputs[index].Content); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func codexInputContentText(raw json.RawMessage) string {
	var direct string
	if err := common2.Unmarshal(raw, &direct); err == nil {
		return direct
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := common2.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if (part.Type == "input_text" || part.Type == "text") && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func hasExactStructuredProperties(raw json.RawMessage, propertyNames ...string) bool {
	if len(raw) == 0 || len(propertyNames) == 0 {
		return false
	}
	wanted := make(map[string]struct{}, len(propertyNames))
	for _, name := range propertyNames {
		wanted[name] = struct{}{}
	}
	var value any
	if err := common2.Unmarshal(raw, &value); err != nil {
		return false
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			if properties, ok := typed["properties"].(map[string]any); ok && len(properties) == len(wanted) {
				matches := true
				for name := range properties {
					if _, found := wanted[name]; !found {
						matches = false
						break
					}
				}
				if matches {
					return true
				}
			}
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
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
