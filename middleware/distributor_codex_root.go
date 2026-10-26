package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	codexRootChannelRouteContextKey     = "codex_root_channel_route_v1"
	codexTurnRouteRoleContextKey        = "codex_turn_route_role_v1"
	codexFallbackChannelKeyContextKey   = "codex_fallback_channel_key_v1"
	codexRootBindingCandidateContextKey = "codex_root_binding_candidate_v1"
)

var errCodexRootModelUnavailable = errors.New("root channel does not support the requested model")

type codexRootChannelRoute struct {
	binding service.CodexRootChannelBinding
	key     string
}

// codexRootBindingCandidate keeps the exact value inspected by
// prepareCodexRootChannelRoute. It lets a fresh-root fallback perform a
// compare-and-delete instead of deleting a route that a concurrent request
// may have replaced in the meantime.
type codexRootBindingCandidate struct {
	userID  int
	rootID  string
	binding service.CodexRootChannelBinding
}

type codexFallbackChannelKey struct {
	channelID   int
	key         string
	index       int
	fingerprint string
}

func firstEnabledCodex2APIPolicyKey(channel *model.Channel, matches func(string, string) bool) (string, int, bool) {
	if channel == nil {
		return "", 0, false
	}
	if !channel.ChannelInfo.IsMultiKey {
		key, err := channel.GetEnabledKeyAt(0)
		if err == nil && matches(channel.GetBaseURL(), key) {
			return key, 0, true
		}
		return "", 0, false
	}
	for index := range channel.GetKeys() {
		key, err := channel.GetEnabledKeyAt(index)
		if err != nil {
			continue
		}
		if matches(channel.GetBaseURL(), key) {
			return key, index, true
		}
	}
	return "", 0, false
}

// A unique configured Codex2API destination may receive auxiliary models that
// are not published for direct user selection. Group, UA pool, explicit channel
// and credential bindings still apply; Codex2API validates the session graph.
func loadCodexBackgroundChannel(c *gin.Context, usingGroup, modelName string) (*model.Channel, string, string, int, bool, error) {
	if c == nil {
		return nil, "", "", 0, false, nil
	}
	uaOnly := common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted)
	explicitID := 0
	if value, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok {
		text, valid := value.(string)
		var err error
		explicitID, err = strconv.Atoi(text)
		if !valid || err != nil || explicitID <= 0 {
			return nil, "", "", 0, false, nil
		}
	}
	var pin ResponsesWSConnectionPin
	if value, ok := c.Get("responses_websocket_pin"); ok {
		pin, _ = value.(ResponsesWSConnectionPin)
		if pin.ChannelID != 0 {
			if explicitID != 0 && explicitID != pin.ChannelID {
				return nil, "", "", 0, false, nil
			}
			explicitID = pin.ChannelID
		}
	}
	groups := []string{usingGroup}
	if usingGroup == "auto" {
		groups = service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	var channels []*model.Channel
	var err error
	if explicitID != 0 {
		var channel *model.Channel
		channel, err = model.CacheGetChannel(explicitID)
		channels = []*model.Channel{channel}
	} else {
		channels, err = model.CacheGetAllChannels()
	}
	if err != nil {
		return nil, "", "", 0, false, err
	}
	var selected *model.Channel
	selectedGroup := ""
	selectedKey := ""
	selectedKeyIndex := 0
	matches := relaychannel.NewCodex2APIPolicyDestinationMatcher()
	for _, channel := range channels {
		if channel == nil || channel.Status != common.ChannelStatusEnabled || channel.UARoutingOnly != uaOnly ||
			!channelSupportsRequestPath(channel, c.Request.URL.Path, modelName) {
			continue
		}
		candidateGroup := ""
		channelGroups := channel.GetGroups()
		for _, group := range groups {
			if slices.Contains(channelGroups, group) && (!uaOnly || service.IsUserAgentRoutingChannelConfigured(c, group, channel.Id)) {
				candidateGroup = group
				break
			}
		}
		if candidateGroup == "" {
			continue
		}
		var key string
		var keyIndex int
		var keyFound bool
		if pin.ChannelID != 0 {
			keyIndex = pin.KeyIndex
			currentKey, keyErr := channel.GetEnabledKeyAt(keyIndex)
			key = currentKey
			keyFound = keyErr == nil && key == pin.Key && matches(channel.GetBaseURL(), key)
		} else {
			key, keyIndex, keyFound = firstEnabledCodex2APIPolicyKey(channel, matches)
		}
		if !keyFound {
			continue
		}
		if selected != nil && selected.Id != channel.Id {
			return nil, "", "", 0, false, nil
		}
		selected = channel
		selectedGroup = candidateGroup
		selectedKey = key
		selectedKeyIndex = keyIndex
	}
	return selected, selectedGroup, selectedKey, selectedKeyIndex, selected != nil, nil
}

func logCodexPassiveRouteFailure(c *gin.Context, stage, modelName string, resolution relaychannel.CodexRootSessionResolution, err error) {
	if c == nil {
		return
	}
	reason := "binding unavailable"
	if err != nil {
		reason = err.Error()
	}
	c.Set("relay_admission_route_stage", stage)
	c.Set("relay_admission_route_reason", reason)
	if c.GetString(common.RequestIdKey) == "" {
		c.Set(common.RequestIdKey, common.NewRequestId())
	}
	association := relaychannel.CodexRequestRootAssociation(c)
	common.SysError(fmt.Sprintf(
		"Codex passive route failed: request_id=%s stage=%s user=%d token=%d group=%s model=%s resolved=%t related=%t source=%s kind=%s subagent=%s reason=%s original_root=%q target_root=%q association=%s candidates=%d",
		c.GetString(common.RequestIdKey),
		stage,
		common.GetContextKeyInt(c, constant.ContextKeyUserId),
		common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		modelName,
		resolution.Resolved,
		resolution.Related,
		resolution.ThreadSource,
		resolution.RequestKind,
		resolution.SubagentKind,
		reason,
		association.OriginalRootID, resolution.RootID, association.Basis, association.Candidates,
	))
}

func codexRootChannelKeyFingerprint(key string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(digest[:])
}

func requestCanUseStoredCodexGroup(c *gin.Context, usingGroup, storedGroup string) bool {
	usingGroup = strings.TrimSpace(usingGroup)
	storedGroup = strings.TrimSpace(storedGroup)
	if usingGroup == "" || storedGroup == "" {
		return false
	}
	if usingGroup != "auto" {
		return usingGroup == storedGroup
	}
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	return slices.Contains(service.GetRequestAutoGroups(c, userGroup), storedGroup)
}

func prepareCodexRootChannelRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, modelName, usingGroup string) (*model.Channel, string, bool, error) {
	if c == nil {
		return nil, "", false, nil
	}
	// Do not let a second preparation attempt in the same request reuse the
	// candidate captured by an earlier route check.
	c.Set(codexRootBindingCandidateContextKey, codexRootBindingCandidate{})
	if !resolution.Resolved || strings.TrimSpace(resolution.RootID) == "" {
		return nil, "", false, nil
	}
	requestUARoutingOnly := common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted)
	userID := c.GetInt(string(constant.ContextKeyUserId))
	binding, found, err := service.LoadCodexRootChannelBindingForRoutingSide(userID, resolution.RootID, requestUARoutingOnly)
	if err != nil {
		return nil, "", false, fmt.Errorf("load root channel binding: %w", err)
	}
	if !found {
		return nil, "", false, nil
	}
	c.Set(codexRootBindingCandidateContextKey, codexRootBindingCandidate{
		userID: userID, rootID: strings.TrimSpace(resolution.RootID), binding: binding,
	})
	if binding.UARoutingOnly != requestUARoutingOnly {
		return nil, "", true, errors.New("root channel binding is outside the current UA routing side")
	}
	if !requestCanUseStoredCodexGroup(c, usingGroup, binding.SelectedGroup) {
		return nil, "", true, errors.New("root channel binding is outside the current group")
	}

	channel, err := model.CacheGetChannel(binding.ChannelID)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
		return nil, "", true, errors.New("root channel is unavailable")
	}
	if !slices.Contains(channel.GetGroups(), binding.SelectedGroup) || channel.UARoutingOnly != binding.UARoutingOnly {
		return nil, "", true, errors.New("root channel configuration changed")
	}
	if channel.UARoutingOnly && !service.IsUserAgentRoutingChannelConfigured(c, binding.SelectedGroup, channel.Id) {
		return nil, "", true, errors.New("root channel is outside the current UA routing pool")
	}
	if !model.IsChannelEnabledForGroupModel(binding.SelectedGroup, modelName, channel.Id) {
		return nil, "", true, errCodexRootModelUnavailable
	}
	if !channelSupportsRequestPath(channel, c.Request.URL.Path, modelName) {
		return nil, "", true, errors.New("root channel does not support this request path")
	}

	key, keyErr := channel.GetEnabledKeyAt(binding.KeyIndex)
	if keyErr != nil || codexRootChannelKeyFingerprint(key) != strings.ToLower(strings.TrimSpace(binding.KeyFingerprint)) {
		return nil, "", true, errors.New("root channel key is unavailable")
	}
	if !relaychannel.IsCodex2APIPolicyDestination(channel.GetBaseURL(), key) {
		return nil, "", true, errors.New("root channel is not an enabled Codex2API policy destination")
	}

	c.Set(codexRootChannelRouteContextKey, codexRootChannelRoute{binding: binding, key: key})
	common.SetContextKey(c, constant.ContextKeyCodexRootChannelPinned, true)
	common.SetContextKey(c, constant.ContextKeyChannelAffinityUserAgentRouted, channel.UARoutingOnly)
	service.MarkCodexRootChannelAffinityUsed(c, binding.SelectedGroup, modelName, channel.Id, resolution.RootID)
	if usingGroup == "auto" {
		common.SetContextKey(c, constant.ContextKeyAutoGroup, binding.SelectedGroup)
	}
	return channel, binding.SelectedGroup, true, nil
}

// codexRootBindingFallbackAllowed limits stale-root recovery to an ordinary
// user request. A related/passive/explicit lineage request must stay on the
// account/channel that created its upstream state; silently selecting another
// channel would turn a deterministic upstream identity error into cross-window
// drift. Token-specific channel selection is likewise explicit and remains
// fail-closed when it disagrees with the root binding.
func codexRootBindingFallbackAllowed(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, rootBindingFound bool, rootErr error) bool {
	if c == nil || rootErr == nil || !rootBindingFound || !resolution.Resolved ||
		resolution.Related || resolution.IdentityConflict || relaychannel.CodexRequestNeedsRootAccountWait(resolution.ThreadSource) || errors.Is(rootErr, errCodexRootModelUnavailable) {
		return false
	}
	return strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyTokenSpecificChannelId)) == ""
}

// clearInvalidCodexRootBindingForFreshRequest performs a compare-and-delete
// for the binding captured by prepareCodexRootChannelRoute. Returning false
// without an error means another request already removed/replaced it; callers
// should prepare the route again before deciding whether to fail closed.
func clearInvalidCodexRootBindingForFreshRequest(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (bool, error) {
	if c == nil || !resolution.Resolved || strings.TrimSpace(resolution.RootID) == "" {
		return false, nil
	}
	raw, found := c.Get(codexRootBindingCandidateContextKey)
	candidate, ok := raw.(codexRootBindingCandidate)
	if !found || !ok || candidate.userID <= 0 || strings.TrimSpace(candidate.rootID) == "" ||
		candidate.binding.ChannelID <= 0 {
		return false, nil
	}
	return service.ClearCodexRootChannelBindingIfMatches(candidate.userID, candidate.rootID, candidate.binding)
}

type codexTurnRootRoute struct {
	mapping    service.CodexTurnRootBinding
	binding    service.CodexRootChannelBinding
	matchedOwn bool
}

type codexTurnRouteRole = service.CodexTurnRouteIdentity

func currentCodexTurnRouteRole(c *gin.Context) (codexTurnRouteRole, bool) {
	if c == nil {
		return codexTurnRouteRole{}, false
	}
	raw, found := c.Get(codexTurnRouteRoleContextKey)
	role, ok := raw.(codexTurnRouteRole)
	return role, found && ok
}

func codexTurnRouteLabelConflict(current, stored string) bool {
	current = strings.TrimSpace(current)
	return current != "" && !strings.EqualFold(current, strings.TrimSpace(stored))
}

func isCodexTurnPhaseRequestKind(requestKind string) bool {
	switch strings.ToLower(strings.TrimSpace(requestKind)) {
	case "turn", "compact", "compaction":
		return true
	default:
		return false
	}
}

// Codex uses turn_id for one logical Turn, not for one HTTP request. A user,
// Guardian, or subagent Turn and the compaction request it triggers can
// therefore carry the same turn_id while request_kind changes. Permit only
// that narrow phase transition: the root, ownership class, thread source and
// subagent role remain the stored values, so a known Turn ID cannot acquire a
// different routing privilege.
func codexCompatibleTurnPhaseRole(resolution relaychannel.CodexRootSessionResolution, mappedRootID string, stored codexTurnRouteRole) (codexTurnRouteRole, bool) {
	if !isCodexTurnPhaseRequestKind(stored.RequestKind) {
		return codexTurnRouteRole{}, false
	}
	if resolution.IdentityConflict || !isCodexTurnPhaseRequestKind(resolution.RequestKind) {
		return codexTurnRouteRole{}, false
	}
	currentRootID := strings.TrimSpace(resolution.RootID)
	mappedRootID = strings.TrimSpace(mappedRootID)
	if currentRootID != "" && !resolution.Resolved {
		return codexTurnRouteRole{}, false
	}
	if currentRootID != "" && !strings.EqualFold(currentRootID, mappedRootID) {
		// Internal children can present their own leaf Thread as a temporary
		// root and then recover the real root through their recorded Turn. This
		// mirrors the replacement rule below without allowing a user fork to
		// collapse back into its source task.
		temporaryRoot := strings.TrimSpace(resolution.ThreadID) == "" ||
			strings.EqualFold(currentRootID, strings.TrimSpace(resolution.ThreadID))
		_, forkedNaming := relaychannel.ClassifyForkedCodexNamingRequest(resolution)
		independentFork := strings.EqualFold(strings.TrimSpace(resolution.ThreadSource), "user") ||
			(strings.TrimSpace(resolution.ForkedFromID) != "" && !forkedNaming)
		if stored.RootOwner || !temporaryRoot || independentFork {
			return codexTurnRouteRole{}, false
		}
	}
	currentSource := strings.TrimSpace(resolution.ThreadSource)
	if currentSource != "" && !strings.EqualFold(currentSource, stored.ThreadSource) {
		return codexTurnRouteRole{}, false
	}
	currentSubagent := strings.TrimSpace(resolution.SubagentKind)
	if currentSubagent != "" && !strings.EqualFold(currentSubagent, stored.SubagentKind) {
		return codexTurnRouteRole{}, false
	}
	if stored.RootOwner {
		if strings.TrimSpace(stored.PassiveFeature) != "" ||
			!strings.EqualFold(strings.TrimSpace(stored.ThreadSource), "user") ||
			strings.TrimSpace(stored.SubagentKind) != "" {
			return codexTurnRouteRole{}, false
		}
	} else if !stored.Related || strings.TrimSpace(stored.ThreadSource) == "" ||
		strings.EqualFold(strings.TrimSpace(stored.ThreadSource), "user") ||
		strings.TrimSpace(stored.PassiveFeature) == "" {
		return codexTurnRouteRole{}, false
	}
	effective := stored
	effective.RequestKind = strings.ToLower(strings.TrimSpace(resolution.RequestKind))
	if stored.RootOwner && resolution.Resolved {
		effective.Related = resolution.Related
	}
	return effective, true
}

func isKnownCodexInternalThreadSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "system", "subagent", "memory_consolidation", "thread_summary",
		"thread_title", "thread_description", "thread_title_reconsideration",
		"ambient_suggestions", "agent_created_thread", "guardian", "guardian_review":
		return true
	default:
		return false
	}
}

type codexTurnRootLookup struct {
	id  string
	own bool
}

func codexTurnAncestryAllowed(resolution relaychannel.CodexRootSessionResolution) bool {
	if strings.EqualFold(strings.TrimSpace(resolution.ThreadSource), "user") {
		return false
	}
	if strings.TrimSpace(resolution.ForkedFromID) != "" {
		_, naming := relaychannel.ClassifyForkedCodexNamingRequest(resolution)
		return naming
	}
	if strings.TrimSpace(resolution.SubagentKind) != "" {
		return true
	}
	if isKnownCodexInternalThreadSource(resolution.ThreadSource) {
		return true
	}
	return resolution.Resolved && resolution.Related
}

func codexTurnRootLookups(resolution relaychannel.CodexRootSessionResolution) []codexTurnRootLookup {
	lookups := make([]codexTurnRootLookup, 0, 3)
	seen := make(map[string]struct{}, 3)
	candidates := []codexTurnRootLookup{{id: resolution.TurnID, own: true}}
	// Only positively identified internal work may inherit an ancestor Turn.
	// An ordinary user fork can carry root_turn_id from its source task and must
	// still open an independent root/account window.
	if codexTurnAncestryAllowed(resolution) {
		candidates = append(candidates,
			codexTurnRootLookup{id: resolution.RootTurnID},
			codexTurnRootLookup{id: resolution.ParentTurnID},
		)
	}
	for _, candidate := range candidates {
		candidate.id = strings.TrimSpace(candidate.id)
		if candidate.id == "" {
			continue
		}
		if _, exists := seen[candidate.id]; exists {
			continue
		}
		seen[candidate.id] = struct{}{}
		lookups = append(lookups, candidate)
	}
	return lookups
}

func sameCodexLineageRoute(left, right codexTurnRootRoute) bool {
	return strings.EqualFold(left.mapping.RootID, right.mapping.RootID) &&
		left.mapping.SelectedGroup == right.mapping.SelectedGroup &&
		left.mapping.UARoutingOnly == right.mapping.UARoutingOnly &&
		left.mapping.BindingFingerprint == right.mapping.BindingFingerprint &&
		left.binding == right.binding
}

func loadCodexTurnRootRouteOnce(ctx context.Context, userID int, resolution relaychannel.CodexRootSessionResolution) (codexTurnRootRoute, bool, error) {
	var resolved codexTurnRootRoute
	foundAny := false
	for _, lookup := range codexTurnRootLookups(resolution) {
		mapping, binding, found, err := service.ResolveCodexTurnRootBinding(ctx, userID, lookup.id)
		if err != nil {
			return codexTurnRootRoute{}, false, fmt.Errorf("resolve Codex turn root binding: %w", err)
		}
		if !found {
			continue
		}
		candidate := codexTurnRootRoute{mapping: mapping, binding: binding, matchedOwn: lookup.own}
		if foundAny && !sameCodexLineageRoute(candidate, resolved) {
			return codexTurnRootRoute{}, false, errors.New("Codex turn lineage resolves to conflicting root bindings")
		}
		if !foundAny || lookup.own {
			resolved = candidate
		}
		foundAny = true
	}
	return resolved, foundAny, nil
}

func codexTurnLineageRequiredForRouting(resolution relaychannel.CodexRootSessionResolution) bool {
	if !resolution.Resolved {
		return true
	}
	threadSource := strings.TrimSpace(resolution.ThreadSource)
	return !resolution.Related && threadSource != "" && !strings.EqualFold(threadSource, "user")
}

func loadCodexTurnRootRoute(c *gin.Context, userID int, resolution relaychannel.CodexRootSessionResolution) (codexTurnRootRoute, bool, error) {
	requestContext := context.Background()
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	return loadCodexTurnRootRouteOnce(requestContext, userID, resolution)
}

func applyCodexTurnRootRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, route codexTurnRootRoute) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	mappedRootID := strings.TrimSpace(route.mapping.RootID)
	if mappedRootID == "" {
		return resolution, "", true, service.ErrCodexTurnRootBindingInvalid
	}
	storedRole := route.mapping.RouteIdentity()
	effectiveRole := storedRole
	if resolution.IndependentRoot && storedRole.RootOwner && strings.EqualFold(mappedRootID, resolution.RootID) {
		effectiveRole.Related = false
		effectiveRole.PassiveFeature = ""
	}
	if route.matchedOwn && (codexTurnRouteLabelConflict(resolution.ThreadSource, route.mapping.ThreadSource) ||
		codexTurnRouteLabelConflict(resolution.RequestKind, route.mapping.RequestKind) ||
		codexTurnRouteLabelConflict(resolution.SubagentKind, route.mapping.SubagentKind)) {
		var phaseCompatible bool
		effectiveRole, phaseCompatible = codexCompatibleTurnPhaseRole(resolution, mappedRootID, storedRole)
		if !phaseCompatible {
			return resolution, "", true, errors.New("Codex turn retry conflicts with its recorded request role")
		}
	}
	if resolution.Resolved && strings.TrimSpace(resolution.RootID) != "" &&
		!strings.EqualFold(strings.TrimSpace(resolution.RootID), mappedRootID) {
		threadID := strings.TrimSpace(resolution.ThreadID)
		temporaryRoot := threadID == "" || strings.EqualFold(strings.TrimSpace(resolution.RootID), threadID)
		_, forkedNaming := relaychannel.ClassifyForkedCodexNamingRequest(resolution)
		independentFork := strings.EqualFold(strings.TrimSpace(resolution.ThreadSource), "user") ||
			(strings.TrimSpace(resolution.ForkedFromID) != "" && !forkedNaming)
		mayReplaceTemporaryRoot := temporaryRoot && !independentFork &&
			((route.matchedOwn && !route.mapping.RootOwner) ||
				(!route.matchedOwn && codexTurnAncestryAllowed(resolution)))
		if !mayReplaceTemporaryRoot {
			return resolution, "", true, errors.New("Codex turn binding conflicts with explicit root session")
		}
	}

	common.SetContextKey(c, constant.ContextKeyChannelAffinityUserAgentRouted, route.binding.UARoutingOnly)
	role := codexTurnRouteRole{
		Related: true, PassiveFeature: "related_internal",
		ThreadSource: strings.TrimSpace(resolution.ThreadSource),
		RequestKind:  strings.TrimSpace(resolution.RequestKind),
		SubagentKind: strings.TrimSpace(resolution.SubagentKind),
	}
	if route.matchedOwn {
		// The stored identity remains the claim/promotion value for this Turn.
		// effectiveRole may describe a later compaction phase of the same Turn
		// and is used only for this request's signed policy metadata.
		c.Set(codexTurnRouteRoleContextKey, storedRole)
		role = effectiveRole
	} else if strings.EqualFold(strings.TrimSpace(resolution.ThreadSource), "system") {
		role.PassiveFeature = "system_passive"
	}
	if !role.Related {
		role.PassiveFeature = ""
	} else if role.PassiveFeature == "" && !role.RootOwner {
		role.PassiveFeature = "related_internal"
	}
	if !route.matchedOwn {
		c.Set(codexTurnRouteRoleContextKey, role)
	}
	if !relaychannel.SetCodexTurnRootSessionOverride(c, mappedRootID, role.Related, role.PassiveFeature, role.ThreadSource, role.RequestKind, role.SubagentKind) {
		return resolution, role.PassiveFeature, true, errors.New("invalid Codex turn root session override")
	}
	resolution.RootID = mappedRootID
	resolution.Resolved = true
	resolution.Related = role.Related
	resolution.ThreadSource = role.ThreadSource
	resolution.RequestKind = role.RequestKind
	resolution.SubagentKind = role.SubagentKind
	return resolution, role.PassiveFeature, true, nil
}

func resolveCodexUserRootRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (relaychannel.CodexRootSessionResolution, error) {
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if resolution.IdentityConflict && (strings.TrimSpace(resolution.TurnID) != "" ||
		strings.TrimSpace(resolution.RootTurnID) != "" || strings.TrimSpace(resolution.ParentTurnID) != "" ||
		strings.TrimSpace(resolution.ForkedFromID) != "") {
		return resolution, errors.New("Codex session identity is conflicting")
	}
	if resolution.TurnLineageConflict && codexTurnLineageRequiredForRouting(resolution) {
		return resolution, errors.New("Codex turn lineage is conflicting or malformed")
	}
	ordinaryFork := strings.TrimSpace(resolution.ForkedFromID) != "" &&
		strings.EqualFold(strings.TrimSpace(resolution.RootID), strings.TrimSpace(resolution.ThreadID))
	if ordinaryFork {
		// Unknown future source labels on a root/leaf-equal fork are treated as a
		// user-owned window. Normalize before looking up the Turn so the first
		// request and an otherwise identical retry compare against the same role,
		// and so ancestor Turn IDs cannot collapse the fork into its source task.
		threadSource := strings.TrimSpace(resolution.ThreadSource)
		if !strings.EqualFold(threadSource, "user") && !isKnownCodexInternalThreadSource(threadSource) {
			role := codexTurnRouteRole{
				RootOwner: true, Related: true, ThreadSource: "user",
				RequestKind: strings.TrimSpace(resolution.RequestKind),
			}
			c.Set(codexTurnRouteRoleContextKey, role)
			if !relaychannel.SetCodexTurnRootSessionOverride(c, resolution.RootID, true, "", "user", role.RequestKind, "") {
				return resolution, errors.New("invalid independent Codex fork root override")
			}
			resolution.Related = true
			resolution.ThreadSource = "user"
			resolution.SubagentKind = ""
		}
	}
	// Only user turns retain local channel affinity. Account ownership and
	// background parent/turn resolution are delegated to Codex2API.
	route, found, err := loadCodexTurnRootRoute(c, userID, resolution)
	if err != nil || !found {
		return resolution, err
	}
	resolved, _, _, err := applyCodexTurnRootRoute(c, resolution, route)
	return resolved, err
}

func pinnedCodexRootChannelKey(c *gin.Context, channel *model.Channel) (string, int, bool, error) {
	if c == nil || channel == nil {
		return "", 0, false, nil
	}
	raw, found := c.Get(codexRootChannelRouteContextKey)
	if !found {
		return "", 0, false, nil
	}
	route, ok := raw.(codexRootChannelRoute)
	if !ok || route.binding.ChannelID != channel.Id {
		return "", 0, true, errors.New("root channel route changed before dispatch")
	}
	if codexRootChannelKeyFingerprint(route.key) != strings.ToLower(strings.TrimSpace(route.binding.KeyFingerprint)) {
		return "", 0, true, errors.New("root channel key changed before dispatch")
	}
	return route.key, route.binding.KeyIndex, true, nil
}

func selectedCodexChannelBinding(c *gin.Context) (int, int, service.CodexRootChannelBinding, bool) {
	userID, tokenID, binding, ok, _ := inspectSelectedCodexChannelBinding(c)
	return userID, tokenID, binding, ok
}

func inspectSelectedCodexChannelBinding(c *gin.Context) (int, int, service.CodexRootChannelBinding, bool, string) {
	if c == nil {
		return 0, 0, service.CodexRootChannelBinding{}, false, "context_unavailable"
	}
	userID := c.GetInt(string(constant.ContextKeyUserId))
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	key := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	if userID <= 0 {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "user_unavailable"
	}
	if channelID <= 0 {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "channel_unavailable"
	}
	if strings.TrimSpace(key) == "" {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "channel_key_unavailable"
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "channel_cache_unavailable"
	}
	if channel.Status != common.ChannelStatusEnabled {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "channel_disabled"
	}
	if policyStatus := relaychannel.Codex2APIPolicyDestinationStatus(channel.GetBaseURL(), key); policyStatus != "matched" {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, policyStatus
	}
	selectedGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if selectedGroup == "auto" {
		selectedGroup = common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	}
	if selectedGroup == "" {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "selected_group_unavailable"
	}
	if !slices.Contains(channel.GetGroups(), selectedGroup) {
		return userID, tokenID, service.CodexRootChannelBinding{}, false, "selected_group_not_on_channel"
	}
	binding := service.CodexRootChannelBinding{
		ChannelID:      channelID,
		SelectedGroup:  selectedGroup,
		KeyIndex:       common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
		KeyFingerprint: codexRootChannelKeyFingerprint(key),
		UARoutingOnly:  channel.UARoutingOnly,
	}
	return userID, tokenID, binding, true, "matched"
}

func codexChannelBaseURLHost(c *gin.Context) string {
	if c == nil {
		return ""
	}
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(channel.GetBaseURL()))
	if err != nil || parsed == nil {
		return ""
	}
	return parsed.Hostname()
}

func logSkippedCodexRootBridge(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel, reason string) {
	if c == nil {
		return
	}
	common.SysError(fmt.Sprintf(
		"Codex root bridge skipped: user=%d token=%d channel=%d group=%s model=%s resolved=%t related=%t source=%s kind=%s subagent=%s ua_routed=%t base_host=%s reason=%s",
		common.GetContextKeyInt(c, constant.ContextKeyUserId),
		common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		requestModel,
		resolution.Resolved,
		resolution.Related,
		resolution.ThreadSource,
		resolution.RequestKind,
		resolution.SubagentKind,
		common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted),
		codexChannelBaseURLHost(c),
		reason,
	))
}

func isCodexRecentMainRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) bool {
	if c == nil {
		return false
	}
	if role, found := currentCodexTurnRouteRole(c); found {
		return role.RootOwner
	}
	threadSource := strings.ToLower(strings.TrimSpace(resolution.ThreadSource))
	// Only an ordinary user root may own and publish a durable channel binding.
	// Any non-user source is an internal root or child; allowing a future source
	// label through here would make a later request pin to that background job.
	if threadSource != "" && threadSource != "user" {
		return false
	}
	// Context compaction can give the user-visible main task a coherent
	// parent/leaf graph, which makes Related true even though this is still the
	// route that owns the root session. Accept that shape only when Codex
	// explicitly labels it as user-sourced. Unlabelled related turns remain
	// excluded so a derived request cannot overwrite the main channel bridge.
	if resolution.Related && threadSource != "user" {
		return false
	}
	return true
}

func selectedCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (int, int, string, service.CodexRootChannelBinding, bool) {
	if !resolution.Resolved || strings.TrimSpace(resolution.RootID) == "" || !isCodexRecentMainRoute(c, resolution) {
		return 0, 0, "", service.CodexRootChannelBinding{}, false
	}
	userID, tokenID, binding, ok := selectedCodexChannelBinding(c)
	if !ok {
		return 0, 0, "", service.CodexRootChannelBinding{}, false
	}
	return userID, tokenID, resolution.RootID, binding, true
}

func selectedCodexTurnRootBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (int, string, string, service.CodexRootChannelBinding, service.CodexTurnRouteIdentity, bool) {
	turnID := strings.TrimSpace(resolution.TurnID)
	rootID := strings.TrimSpace(resolution.RootID)
	if c == nil || turnID == "" || rootID == "" || !resolution.Resolved || resolution.TurnLineageConflict {
		return 0, "", "", service.CodexRootChannelBinding{}, service.CodexTurnRouteIdentity{}, false
	}
	rootOwner := isCodexRecentMainRoute(c, resolution)
	if !rootOwner && !common.GetContextKeyBool(c, constant.ContextKeyCodexRootChannelPinned) {
		return 0, "", "", service.CodexRootChannelBinding{}, service.CodexTurnRouteIdentity{}, false
	}
	userID, _, binding, ok := selectedCodexChannelBinding(c)
	if !ok {
		return 0, "", "", service.CodexRootChannelBinding{}, service.CodexTurnRouteIdentity{}, false
	}
	role := service.CodexTurnRouteIdentity{
		RootOwner: rootOwner, Related: resolution.Related,
		ThreadSource: strings.TrimSpace(resolution.ThreadSource),
		RequestKind:  strings.TrimSpace(resolution.RequestKind),
		SubagentKind: strings.TrimSpace(resolution.SubagentKind),
	}
	if storedRole, found := currentCodexTurnRouteRole(c); found {
		return userID, turnID, rootID, binding, storedRole, true
	}
	if !rootOwner {
		role.Related = true
		role.PassiveFeature = strings.TrimSpace(relaychannel.CodexPassiveRootSessionOverrideFeature(c))
		if role.PassiveFeature == "" {
			role.PassiveFeature = "related_internal"
			if strings.EqualFold(strings.TrimSpace(resolution.ThreadSource), "system") {
				role.PassiveFeature = "system_passive"
			}
		}
	}
	if rootOwner {
		role.PassiveFeature = ""
	}
	return userID, turnID, rootID, binding, role, true
}

type codexProvisionalLineageClaim struct {
	kind          string
	userID        int
	identifier    string
	expected      service.CodexTurnRootBinding
	rollbackToken string
}

func (claim codexProvisionalLineageClaim) rollback() (bool, error) {
	switch claim.kind {
	case "turn":
		return service.ReleaseProvisionalCodexTurnRootBinding(claim.userID, claim.identifier, claim.expected, claim.rollbackToken)
	case "thread":
		return service.ReleaseProvisionalCodexThreadRootBinding(claim.userID, claim.identifier, claim.expected, claim.rollbackToken)
	default:
		return false, nil
	}
}

func rollbackProvisionalCodexLineageClaims(requestModel, failedStage string, claims ...codexProvisionalLineageClaim) {
	for index := len(claims) - 1; index >= 0; index-- {
		claim := claims[index]
		if claim.kind == "" {
			continue
		}
		if _, err := claim.rollback(); err != nil {
			common.SysError(fmt.Sprintf("rollback provisional Codex %s binding failed: stage=%s user=%d model=%s err=%v",
				claim.kind, failedStage, claim.userID, requestModel, err))
		}
	}
}

func claimProvisionalCodexTurnRootBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (codexProvisionalLineageClaim, error) {
	userID, turnID, rootID, binding, role, ok := selectedCodexTurnRootBinding(c, resolution)
	if !ok {
		return codexProvisionalLineageClaim{}, nil
	}
	winner, won, created, rollbackToken, err := service.ClaimProvisionalCodexTurnRootBinding(userID, turnID, rootID, binding, role)
	if err != nil {
		return codexProvisionalLineageClaim{}, err
	}
	claim := codexProvisionalLineageClaim{}
	if created {
		claim = codexProvisionalLineageClaim{
			kind: "turn", userID: userID, identifier: turnID,
			expected: winner, rollbackToken: rollbackToken,
		}
	}
	if !won {
		return codexProvisionalLineageClaim{}, fmt.Errorf("%w: requested_role=%+v winner=%+v", service.ErrCodexTurnRootBindingConflict, role, winner.RouteIdentity())
	}
	if winner.RootID != rootID || winner.SelectedGroup != binding.SelectedGroup ||
		winner.UARoutingOnly != binding.UARoutingOnly ||
		winner.BindingFingerprint != service.CodexRootChannelBindingFingerprint(binding) ||
		winner.RouteIdentity() != role {
		_, rollbackErr := claim.rollback()
		return codexProvisionalLineageClaim{}, errors.Join(service.ErrCodexTurnRootBindingConflict, rollbackErr)
	}
	return claim, nil
}

func recordCodexTurnRootBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	userID, turnID, rootID, binding, role, ok := selectedCodexTurnRootBinding(c, resolution)
	if !ok {
		return
	}
	if err := service.StoreCodexTurnRootBinding(userID, turnID, rootID, binding, role); err != nil {
		common.SysError(fmt.Sprintf("store Codex turn root binding failed: user=%d channel=%d model=%s err=%v", userID, binding.ChannelID, requestModel, err))
	}
}

func selectedCodexThreadRootBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (int, string, string, service.CodexRootChannelBinding, bool) {
	threadID := strings.TrimSpace(resolution.ThreadID)
	rootID := strings.TrimSpace(resolution.RootID)
	if c == nil || threadID == "" || rootID == "" || !resolution.Resolved || resolution.IdentityConflict {
		return 0, "", "", service.CodexRootChannelBinding{}, false
	}
	if !isCodexRecentMainRoute(c, resolution) && !common.GetContextKeyBool(c, constant.ContextKeyCodexRootChannelPinned) {
		return 0, "", "", service.CodexRootChannelBinding{}, false
	}
	userID, _, binding, ok := selectedCodexChannelBinding(c)
	if !ok {
		return 0, "", "", service.CodexRootChannelBinding{}, false
	}
	return userID, threadID, rootID, binding, true
}

func claimProvisionalCodexThreadRootBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (codexProvisionalLineageClaim, error) {
	userID, threadID, rootID, binding, ok := selectedCodexThreadRootBinding(c, resolution)
	if !ok {
		return codexProvisionalLineageClaim{}, nil
	}
	winner, won, created, rollbackToken, err := service.ClaimProvisionalCodexThreadRootBinding(userID, threadID, rootID, binding)
	if err != nil {
		return codexProvisionalLineageClaim{}, err
	}
	claim := codexProvisionalLineageClaim{}
	if created {
		claim = codexProvisionalLineageClaim{
			kind: "thread", userID: userID, identifier: threadID,
			expected: winner, rollbackToken: rollbackToken,
		}
	}
	if !won || winner.RootID != rootID || winner.SelectedGroup != binding.SelectedGroup ||
		winner.UARoutingOnly != binding.UARoutingOnly ||
		winner.BindingFingerprint != service.CodexRootChannelBindingFingerprint(binding) {
		_, rollbackErr := claim.rollback()
		return codexProvisionalLineageClaim{}, errors.Join(service.ErrCodexTurnRootBindingConflict, rollbackErr)
	}
	return claim, nil
}

func recordCodexThreadRootBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	userID, threadID, rootID, binding, ok := selectedCodexThreadRootBinding(c, resolution)
	if !ok {
		return
	}
	if err := service.StoreCodexThreadRootBinding(userID, threadID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("store Codex thread root binding failed: user=%d channel=%d model=%s err=%v", userID, binding.ChannelID, requestModel, err))
	}
}

func claimProvisionalCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) (bool, bool, error) {
	mainRoute := isCodexRecentMainRoute(c, resolution)
	_, _, _, recentOK, bindingReason := inspectSelectedCodexChannelBinding(c)
	if mainRoute && !recentOK {
		logSkippedCodexRootBridge(c, resolution, requestModel, bindingReason)
	}
	userID, _, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution)
	if !ok {
		return false, false, nil
	}
	_, selectedWon, err := service.ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
	if err != nil {
		return false, true, err
	}
	if !selectedWon {
		return true, true, nil
	}
	return false, true, nil
}

func recordCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	if c == nil || c.Writer == nil || c.Writer.Status() >= http.StatusBadRequest {
		return
	}
	userID, _, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution)
	if !ok {
		return
	}
	if err := service.StoreCodexRootChannelBinding(userID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("store Codex root channel binding failed: user=%d channel=%d err=%v", userID, binding.ChannelID, err))
		return
	}
}
