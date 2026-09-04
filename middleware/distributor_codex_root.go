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
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	codexRootChannelRouteContextKey        = "codex_root_channel_route_v1"
	codexPendingPassiveRootAliasContextKey = "codex_pending_passive_root_alias_v1"
	codexTurnRouteRoleContextKey           = "codex_turn_route_role_v1"
	codexRequestArrivalContextKey          = "codex_request_arrival_v1"
	codexRecognizedRootContextKey          = "codex_recognized_root_v1"
	codexFallbackChannelKeyContextKey      = "codex_fallback_channel_key_v1"
	codexRootBindingCandidateContextKey    = "codex_root_binding_candidate_v1"
)

var (
	codexUnlinkedPassiveRootWaitTimeout    = 3 * time.Second
	codexLinkedRootWaitTimeout             = 3 * time.Second
	codexTurnRootWaitTimeout               = 3 * time.Second
	codexThreadRootWaitTimeout             = 3 * time.Second
	codexUnlinkedPassiveRootFallbackWindow = 5 * time.Minute
	waitForRecentCodexRootChannelUpdate    = service.WaitForRecentCodexRootChannelUpdate
	waitForRecentCodexTitleRootUpdate      = service.WaitForCodexTitleRootChannelUpdate
	waitForCodexRootChannelBindingUpdate   = service.WaitForCodexRootChannelBindingUpdate
	waitForCodexTurnRootBindingUpdate      = service.WaitForCodexTurnRootBindingUpdate
	waitForCodexThreadRootBindingUpdate    = service.WaitForCodexThreadRootBindingUpdate
)

type codexRootChannelRoute struct {
	binding service.CodexRootChannelBinding
	key     string
}

type codexPendingPassiveRootAlias struct {
	userID         int
	tokenID        int
	sourceRootID   string
	alias          service.CodexPassiveRootAlias
	claimRequired  bool
	titleCandidate bool
	observation    service.CodexRecentRootChannelCandidate
	cutoff         service.CodexRequestArrival
	temporaryOnly  bool
}

type codexRequestArrivalState struct {
	arrival service.CodexRequestArrival
	err     error
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

func codexPassiveAliasIsTemporary(c *gin.Context) bool {
	if c == nil {
		return false
	}
	raw, found := c.Get(codexPendingPassiveRootAliasContextKey)
	pending, ok := raw.(codexPendingPassiveRootAlias)
	return found && ok && pending.temporaryOnly
}

func captureCodexRequestArrival(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	if _, found := c.Get(codexRequestArrivalContextKey); found {
		return
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	path = strings.ToLower(path)
	if !strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/responses/compact") {
		return
	}
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if userID <= 0 || tokenID <= 0 {
		return
	}
	arrival, err := service.BeginCodexRequestArrival(c.Request.Context(), userID, tokenID)
	c.Set(codexRequestArrivalContextKey, codexRequestArrivalState{arrival: arrival, err: err})
}

func currentCodexRequestArrival(c *gin.Context) (service.CodexRequestArrival, bool, error) {
	if c == nil {
		return service.CodexRequestArrival{}, false, nil
	}
	captureCodexRequestArrival(c)
	raw, found := c.Get(codexRequestArrivalContextKey)
	state, ok := raw.(codexRequestArrivalState)
	if !found || !ok {
		return service.CodexRequestArrival{}, false, nil
	}
	if state.err != nil {
		return service.CodexRequestArrival{}, false, state.err
	}
	return state.arrival, state.arrival.Order > 0 && !state.arrival.ArrivedAt.IsZero(), nil
}

func markCodexRecognizedRoot(c *gin.Context) {
	if c != nil {
		c.Set(codexRecognizedRootContextKey, true)
	}
}

func canPassThroughRecognizedCodexRoot(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) bool {
	return c != nil && common.GetContextKeyBool(c, codexRecognizedRootContextKey) &&
		resolution.Resolved && resolution.Related && strings.TrimSpace(resolution.RootID) != "" &&
		!resolution.IdentityConflict &&
		!common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted)
}

type codexFallbackChannelKey struct {
	channelID   int
	key         string
	index       int
	fingerprint string
}

func firstEnabledCodex2APIPolicyKey(channel *model.Channel) (string, int, bool) {
	if channel == nil {
		return "", 0, false
	}
	if !channel.ChannelInfo.IsMultiKey {
		key, err := channel.GetEnabledKeyAt(0)
		if err == nil && relaychannel.IsCodex2APIPolicyDestination(channel.GetBaseURL(), key) {
			return key, 0, true
		}
		return "", 0, false
	}
	for index := range channel.GetKeys() {
		key, err := channel.GetEnabledKeyAt(index)
		if err != nil {
			continue
		}
		if relaychannel.IsCodex2APIPolicyDestination(channel.GetBaseURL(), key) {
			return key, index, true
		}
	}
	return "", 0, false
}

// loadUniqueRecognizedCodexPassThroughChannel supports deployments with one
// ordinary Codex2API channel. It intentionally ignores model publication for a
// recognized internal request, because title/Guardian/subagent models need not
// be independently exposed as user-selectable abilities.
func loadUniqueRecognizedCodexPassThroughChannel(c *gin.Context, usingGroup, modelName string) (*model.Channel, string, string, int, bool, error) {
	if c == nil || common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted) {
		return nil, "", "", 0, false, nil
	}
	groups := []string{usingGroup}
	if usingGroup == "auto" {
		groups = service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return nil, "", "", 0, false, err
	}
	var selected *model.Channel
	selectedGroup := ""
	selectedKey := ""
	selectedKeyIndex := 0
	for _, storedChannel := range channels {
		if storedChannel == nil {
			continue
		}
		channel, cacheErr := model.CacheGetChannel(storedChannel.Id)
		if cacheErr != nil || channel == nil || channel.Status != common.ChannelStatusEnabled || channel.UARoutingOnly ||
			!channelSupportsRequestPath(channel, c.Request.URL.Path, modelName) {
			continue
		}
		key, keyIndex, keyFound := firstEnabledCodex2APIPolicyKey(channel)
		if !keyFound {
			continue
		}
		candidateGroup := ""
		channelGroups := channel.GetGroups()
		for _, group := range groups {
			if slices.Contains(channelGroups, group) {
				candidateGroup = group
				break
			}
		}
		if candidateGroup == "" {
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
	common.SysError(fmt.Sprintf(
		"Codex passive route failed: stage=%s user=%d token=%d group=%s model=%s resolved=%t related=%t source=%s kind=%s subagent=%s reason=%s",
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

func isLinkedCodexNamingRequest(resolution relaychannel.CodexRootSessionResolution) bool {
	if !resolution.Resolved || !resolution.Related || strings.TrimSpace(resolution.RootID) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(resolution.ThreadSource)) {
	case "thread_title", "thread_description", "thread_title_reconsideration":
		return true
	default:
		return false
	}
}

func isCodexNamingRequest(resolution relaychannel.CodexRootSessionResolution) bool {
	if isLinkedCodexNamingRequest(resolution) {
		return true
	}
	_, unlinkedTitle := relaychannel.ClassifyUnlinkedCodexThreadTitleRequest(resolution)
	return unlinkedTitle
}

func loadLinkedCodexNamingRootBinding(c *gin.Context, userID int, rootID string) (service.CodexRootChannelBinding, bool, error) {
	rootID = strings.TrimSpace(rootID)
	if c == nil || userID <= 0 || rootID == "" {
		return service.CodexRootChannelBinding{}, false, nil
	}
	requestContext := context.Background()
	if c.Request != nil {
		requestContext = c.Request.Context()
	}
	binding, found, err := loadUniqueCodexRootChannelBindingContext(requestContext, userID, rootID)
	if err != nil || found || codexLinkedRootWaitTimeout <= 0 {
		return binding, found, err
	}

	waitContext, cancelWait := context.WithTimeout(requestContext, codexLinkedRootWaitTimeout)
	defer cancelWait()
	for {
		waitErr := waitForCodexRootChannelBindingUpdate(waitContext, userID, rootID, codexLinkedRootWaitTimeout)
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, fmt.Errorf("wait for linked Codex naming root binding: %w", waitErr)
		}
		binding, found, err = loadUniqueCodexRootChannelBindingContext(waitContext, userID, rootID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, fmt.Errorf("load linked Codex naming root binding: %w", err)
		}
		if found {
			return binding, true, nil
		}
		if err := waitContext.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, fmt.Errorf("wait for linked Codex naming root binding: %w", err)
		}
	}
}

func loadRecognizedCodexRootBinding(
	c *gin.Context,
	userID int,
	rootID string,
	uaRoutingOnly bool,
) (service.CodexRootChannelBinding, bool, error) {
	rootID = strings.TrimSpace(rootID)
	if c == nil || userID <= 0 || rootID == "" {
		return service.CodexRootChannelBinding{}, false, nil
	}
	requestContext := context.Background()
	if c.Request != nil {
		requestContext = c.Request.Context()
	}
	binding, found, err := service.LoadCodexRootChannelBindingForRoutingSideContext(requestContext, userID, rootID, uaRoutingOnly)
	if err != nil || found || codexLinkedRootWaitTimeout <= 0 {
		return binding, found, err
	}
	waitContext, cancelWait := context.WithTimeout(requestContext, codexLinkedRootWaitTimeout)
	defer cancelWait()
	deadline := time.Now().Add(codexLinkedRootWaitTimeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return service.CodexRootChannelBinding{}, false, nil
		}
		waitErr := waitForCodexRootChannelBindingUpdate(waitContext, userID, rootID, remaining)
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, fmt.Errorf("wait for recognized Codex root binding: %w", waitErr)
		}
		binding, found, err = service.LoadCodexRootChannelBindingForRoutingSideContext(waitContext, userID, rootID, uaRoutingOnly)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, fmt.Errorf("load recognized Codex root binding: %w", err)
		}
		if found {
			return binding, true, nil
		}
		if err := waitContext.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, err
		}
	}
}

func loadUniqueCodexRootChannelBindingContext(ctx context.Context, userID int, rootID string) (service.CodexRootChannelBinding, bool, error) {
	normal, normalFound, err := service.LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, rootID, false)
	if err != nil {
		return service.CodexRootChannelBinding{}, false, err
	}
	uaOnly, uaFound, err := service.LoadCodexRootChannelBindingForRoutingSideContext(ctx, userID, rootID, true)
	if err != nil {
		return service.CodexRootChannelBinding{}, false, err
	}
	if normalFound && uaFound {
		return service.CodexRootChannelBinding{}, false, errors.New("Codex root channel binding is ambiguous across UA routing sides")
	}
	if normalFound {
		return normal, true, nil
	}
	if uaFound {
		return uaOnly, true, nil
	}
	return service.CodexRootChannelBinding{}, false, nil
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
	if isIndependentCodexInternalRoot(resolution) {
		// Independent background roots are ordinary scheduling units. Ignoring
		// any legacy binding for the same ID also makes the behavior effective
		// immediately after upgrading from the old over-broad bridge logic.
		return nil, "", false, nil
	}
	passiveInternal := codexPassiveRouteAuthorized(c, resolution)
	requestUARoutingOnly := common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted)
	userID := c.GetInt(string(constant.ContextKeyUserId))
	var binding service.CodexRootChannelBinding
	var found bool
	var err error
	if isLinkedCodexNamingRequest(resolution) {
		binding, found, err = loadLinkedCodexNamingRootBinding(c, userID, resolution.RootID)
		if found {
			requestUARoutingOnly = binding.UARoutingOnly
		}
	} else if passiveInternal && common.GetContextKeyBool(c, codexRecognizedRootContextKey) {
		binding, found, err = loadRecognizedCodexRootBinding(c, userID, resolution.RootID, requestUARoutingOnly)
	} else {
		binding, found, err = service.LoadCodexRootChannelBindingForRoutingSide(userID, resolution.RootID, requestUARoutingOnly)
	}
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
	if !channelSupportsRequestPath(channel, c.Request.URL.Path, modelName) {
		return nil, "", true, errors.New("root channel does not support this request path")
	}
	if !passiveInternal && !model.IsChannelEnabledForGroupModel(binding.SelectedGroup, modelName, channel.Id) {
		return nil, "", true, errors.New("root channel does not support the requested model")
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
		resolution.Related || resolution.IdentityConflict {
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

func isIndependentCodexInternalRoot(resolution relaychannel.CodexRootSessionResolution) bool {
	threadSource := strings.TrimSpace(resolution.ThreadSource)
	return resolution.Resolved && !resolution.Related && strings.TrimSpace(resolution.RootID) != "" &&
		threadSource != "" && !strings.EqualFold(threadSource, "user") && !strings.EqualFold(threadSource, "system")
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
		"thread_title", "thread_description", "thread_title_reconsideration":
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

func codexTurnRootFallbackRequired(resolution relaychannel.CodexRootSessionResolution) bool {
	// A standalone thread_title already has a purpose-built five-second bridge.
	// Try any exact Turn mapping once, but an unknown ancestor must not prevent
	// the title bridge from resolving the fresh user root.
	if _, unlinkedTitle := relaychannel.ClassifyUnlinkedCodexThreadTitleRequest(resolution); unlinkedTitle {
		return false
	}
	if !codexTurnAncestryAllowed(resolution) {
		return false
	}
	if strings.TrimSpace(resolution.RootTurnID) == "" && strings.TrimSpace(resolution.ParentTurnID) == "" {
		return false
	}
	if resolution.Resolved && resolution.Related {
		return false
	}
	return true
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
	route, found, err := loadCodexTurnRootRouteOnce(requestContext, userID, resolution)
	if errors.Is(err, service.ErrCodexTurnRootRouteUnavailable) && resolution.Resolved && resolution.Related &&
		strings.TrimSpace(resolution.RootID) != "" && !resolution.IdentityConflict {
		// The Turn/Thread lineage is known, but its root channel record may be
		// momentarily absent while the parent request is publishing it. Let the
		// normal recognized-root lookup wait on the root ID instead of converting
		// this transient state into an early 503.
		if c != nil {
			c.Set(codexRecognizedRootContextKey, true)
		}
		return codexTurnRootRoute{}, false, nil
	}
	if err != nil || found || !codexTurnRootFallbackRequired(resolution) || codexTurnRootWaitTimeout <= 0 {
		return route, found, err
	}

	waitTurnID := strings.TrimSpace(resolution.RootTurnID)
	if waitTurnID == "" {
		waitTurnID = strings.TrimSpace(resolution.ParentTurnID)
	}
	waitContext, cancelWait := context.WithTimeout(requestContext, codexTurnRootWaitTimeout)
	defer cancelWait()
	for {
		waitErr := waitForCodexTurnRootBindingUpdate(waitContext, userID, waitTurnID, codexTurnRootWaitTimeout)
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) && requestContext.Err() == nil {
				return codexTurnRootRoute{}, false, nil
			}
			return codexTurnRootRoute{}, false, fmt.Errorf("wait for Codex turn root binding: %w", waitErr)
		}
		if err := waitContext.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && requestContext.Err() == nil {
				return codexTurnRootRoute{}, false, nil
			}
			return codexTurnRootRoute{}, false, fmt.Errorf("wait for Codex turn root binding: %w", err)
		}
		route, found, err = loadCodexTurnRootRouteOnce(waitContext, userID, resolution)
		if err != nil || found {
			return route, found, err
		}
	}
}

func applyCodexTurnRootRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, route codexTurnRootRoute) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	mappedRootID := strings.TrimSpace(route.mapping.RootID)
	if mappedRootID == "" {
		return resolution, "", true, service.ErrCodexTurnRootBindingInvalid
	}
	storedRole := route.mapping.RouteIdentity()
	effectiveRole := storedRole
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

func loadCodexThreadRootRoute(c *gin.Context, userID int, threadID string) (codexTurnRootRoute, bool, error) {
	requestContext := context.Background()
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	load := func(ctx context.Context) (codexTurnRootRoute, bool, error) {
		mapping, binding, found, err := service.ResolveCodexThreadRootBinding(ctx, userID, threadID)
		return codexTurnRootRoute{mapping: mapping, binding: binding}, found, err
	}
	route, found, err := load(requestContext)
	if err != nil || found || codexThreadRootWaitTimeout <= 0 {
		return route, found, err
	}
	waitContext, cancelWait := context.WithTimeout(requestContext, codexThreadRootWaitTimeout)
	defer cancelWait()
	for {
		waitErr := waitForCodexThreadRootBindingUpdate(waitContext, userID, threadID, codexThreadRootWaitTimeout)
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) && requestContext.Err() == nil {
				return codexTurnRootRoute{}, false, nil
			}
			return codexTurnRootRoute{}, false, fmt.Errorf("wait for Codex thread root binding: %w", waitErr)
		}
		if err := waitContext.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && requestContext.Err() == nil {
				return codexTurnRootRoute{}, false, nil
			}
			return codexTurnRootRoute{}, false, fmt.Errorf("wait for Codex thread root binding: %w", err)
		}
		route, found, err = load(waitContext)
		if err != nil || found {
			return route, found, err
		}
	}
}

func resolveForkedCodexNamingRoot(c *gin.Context, userID int, resolution relaychannel.CodexRootSessionResolution, feature string, turnRoute codexTurnRootRoute, turnRouteFound bool) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	sourceThreadID := strings.TrimSpace(resolution.ForkedFromID)
	requestContext := context.Background()
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	mapping, threadBinding, found, err := service.ResolveCodexThreadRootBinding(requestContext, userID, sourceThreadID)
	if err != nil {
		return resolution, feature, true, fmt.Errorf("load forked Codex naming thread binding: %w", err)
	}
	sourceRoute := codexTurnRootRoute{mapping: mapping, binding: threadBinding}
	if !found {
		// Upgrade compatibility: roots recorded before thread lineage support may
		// only have the root-session binding. This fallback is valid precisely
		// when the source Thread ID is itself the root ID.
		binding, rootFound, rootErr := loadUniqueCodexRootChannelBindingContext(requestContext, userID, sourceThreadID)
		if rootErr != nil {
			return resolution, feature, true, fmt.Errorf("load forked Codex naming root binding: %w", rootErr)
		}
		if rootFound {
			sourceRoute = codexTurnRootRoute{
				mapping: service.CodexTurnRootBinding{
					RootID: sourceThreadID, SelectedGroup: binding.SelectedGroup,
					BindingFingerprint: service.CodexRootChannelBindingFingerprint(binding),
					UARoutingOnly:      binding.UARoutingOnly,
				},
				binding: binding,
			}
			found = true
		}
	}
	if !found {
		sourceRoute, found, err = loadCodexThreadRootRoute(c, userID, sourceThreadID)
		if err != nil {
			return resolution, feature, true, fmt.Errorf("wait for forked Codex naming thread binding: %w", err)
		}
		if !found {
			binding, rootFound, rootErr := loadLinkedCodexNamingRootBinding(c, userID, sourceThreadID)
			if rootErr != nil {
				return resolution, feature, true, fmt.Errorf("wait for forked Codex naming root binding: %w", rootErr)
			}
			if !rootFound {
				return resolution, feature, true, errors.New("forked Codex naming thread binding is unavailable")
			}
			sourceRoute = codexTurnRootRoute{
				mapping: service.CodexTurnRootBinding{
					RootID: sourceThreadID, SelectedGroup: binding.SelectedGroup,
					BindingFingerprint: service.CodexRootChannelBindingFingerprint(binding),
					UARoutingOnly:      binding.UARoutingOnly,
				},
				binding: binding,
			}
		}
	}
	if turnRouteFound && !sameCodexLineageRoute(sourceRoute, turnRoute) {
		return resolution, feature, true, errors.New("Codex turn binding conflicts with naming fork source thread")
	}
	common.SetContextKey(c, constant.ContextKeyChannelAffinityUserAgentRouted, sourceRoute.binding.UARoutingOnly)
	return applyUnlinkedCodexPassiveRoot(c, resolution, sourceRoute.mapping.RootID, feature)
}

// resolveUnlinkedCodexPassiveRoot pins an explicitly related child to its exact
// root. The independent system thread used for project metadata may recover a
// sole candidate on its own routing side. A native thread_title has no parent
// graph, so it uses a separate five-second candidate set that must be unique
// across both routing sides. Other independent internal roots schedule normally.
func resolveUnlinkedCodexPassiveRoot(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if resolution.IdentityConflict && (strings.TrimSpace(resolution.TurnID) != "" ||
		strings.TrimSpace(resolution.RootTurnID) != "" || strings.TrimSpace(resolution.ParentTurnID) != "" ||
		strings.TrimSpace(resolution.ForkedFromID) != "") {
		return resolution, "", true, errors.New("Codex session identity is conflicting")
	}
	if resolution.TurnLineageConflict && codexTurnLineageRequiredForRouting(resolution) {
		return resolution, "", true, errors.New("Codex turn lineage is conflicting or malformed")
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
				return resolution, "", true, errors.New("invalid independent Codex fork root override")
			}
			resolution.Related = true
			resolution.ThreadSource = "user"
			resolution.SubagentKind = ""
		}
	}
	turnRoute, turnRouteFound, turnRouteErr := loadCodexTurnRootRoute(c, userID, resolution)
	if turnRouteErr != nil {
		return resolution, "", true, turnRouteErr
	}
	if feature, forkedNaming := relaychannel.ClassifyForkedCodexNamingRequest(resolution); forkedNaming {
		resolved, resolvedFeature, strict, err := resolveForkedCodexNamingRoot(c, userID, resolution, feature, turnRoute, turnRouteFound)
		if err == nil && resolved.Resolved && resolved.Related {
			markCodexRecognizedRoot(c)
		}
		return resolved, resolvedFeature, strict, err
	}
	if turnRouteFound {
		markCodexRecognizedRoot(c)
		return applyCodexTurnRootRoute(c, resolution, turnRoute)
	}
	if codexTurnRootFallbackRequired(resolution) {
		return resolution, "", true, errors.New("Codex turn root binding is unavailable")
	}
	if ordinaryFork {
		// A root/leaf-equal fork is a new user window unless the stricter naming
		// classifier above proved otherwise. Unknown or missing source labels must
		// not let the generic linked classifier collapse it into the old task.
		threadSource := strings.TrimSpace(resolution.ThreadSource)
		if strings.EqualFold(threadSource, "user") {
			return resolution, "", false, nil
		}
	}
	if feature, linked := relaychannel.ClassifyLinkedCodexPassiveInternalRequest(resolution); linked {
		// A coherent explicit parent graph is intentionally scoped to the NewAPI
		// user and root, not to one API key. This lets the same user resume a
		// Codex task after rotating keys; prepareCodexRootChannelRoute still
		// rechecks the current group, UA side and any token-specific channel.
		if !relaychannel.SetCodexPassiveRootSessionOverride(c, resolution.RootID, feature) {
			return resolution, feature, true, errors.New("invalid linked Codex passive root session override")
		}
		markCodexRecognizedRoot(c)
		return resolution, feature, true, nil
	}
	feature, titleCandidate := relaychannel.ClassifyUnlinkedCodexThreadTitleRequest(resolution)
	classified := titleCandidate
	if !classified {
		feature, classified = relaychannel.ClassifyUnlinkedCodexSystemRequest(resolution)
	}
	if !classified {
		feature, classified = relaychannel.ClassifyUnlinkedCodexThreadSummaryRequest(resolution)
	}
	if !classified {
		return resolution, "", false, nil
	}
	// The wider predecessor search is deliberately limited to the unlinked
	// project/system passive request.  Title and summary requests have their
	// own short-lived bridges (or no reliable predecessor at all); allowing
	// them to reach back five minutes would make an old window look like the
	// current one and is exactly the cross-window drift this fallback is meant
	// to avoid.
	allowExtendedPredecessor := !titleCandidate && feature == "system_passive"
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	sourceRootID := strings.TrimSpace(resolution.RootID)
	uaRoutingOnly := common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted)
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	requestContext := c.Request.Context()
	alias, found, err := service.LoadCodexPassiveRootAlias(requestContext, userID, tokenID, sourceRootID)
	if err != nil {
		return resolution, feature, true, fmt.Errorf("load Codex passive root alias: %w", err)
	}
	if found {
		if (!titleCandidate && alias.UARoutingOnly != uaRoutingOnly) || !requestCanUseStoredCodexGroup(c, usingGroup, alias.SelectedGroup) {
			return resolution, feature, true, errors.New("Codex passive root alias is outside the current routing scope")
		}
		binding, bindingFound, bindingErr := service.LoadCodexRootChannelBindingForRoutingSide(userID, alias.RootID, alias.UARoutingOnly)
		if bindingErr != nil {
			return resolution, feature, true, fmt.Errorf("load aliased Codex root channel binding: %w", bindingErr)
		}
		if !bindingFound || binding.UARoutingOnly != alias.UARoutingOnly || binding.SelectedGroup != alias.SelectedGroup ||
			service.CodexRootChannelBindingFingerprint(binding) != alias.BindingFingerprint {
			return resolution, feature, true, errors.New("aliased Codex root channel binding is unavailable")
		}
		c.Set(codexPendingPassiveRootAliasContextKey, codexPendingPassiveRootAlias{
			userID: userID, tokenID: tokenID, sourceRootID: sourceRootID, alias: alias,
			titleCandidate: titleCandidate, temporaryOnly: alias.Temporary,
		})
		return applyUnlinkedCodexPassiveRoot(c, resolution, alias.RootID, feature)
	}
	var cutoff service.CodexRequestArrival
	if !titleCandidate {
		var cutoffFound bool
		cutoff, cutoffFound, err = currentCodexRequestArrival(c)
		if err != nil {
			return resolution, feature, true, fmt.Errorf("reserve Codex request arrival: %w", err)
		}
		if !cutoffFound {
			return resolution, feature, true, errors.New("Codex request arrival is unavailable")
		}
	}

	deadline := time.Now().Add(codexUnlinkedPassiveRootWaitTimeout)
	waitContext, cancelWait := context.WithTimeout(requestContext, codexUnlinkedPassiveRootWaitTimeout)
	defer cancelWait()
	var soleCandidate *service.CodexRecentRootChannelCandidate
	// Once the strict predecessor wait expires, an unlinked system request may
	// use one bounded, lower-confidence predecessor lookup. This lookup remains
	// ordered (only arrivals before the request) and never considers title
	// candidates, which have their own five-second bridge.
	loadExtendedPredecessor := func() (relaychannel.CodexRootSessionResolution, string, bool, error) {
		if !allowExtendedPredecessor {
			return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
		}
		candidate, candidateFound, loadErr := service.LoadLatestCodexRootChannelObservationBeforeWithin(
			requestContext, userID, tokenID, uaRoutingOnly, cutoff, codexUnlinkedPassiveRootFallbackWindow,
		)
		if loadErr != nil {
			return resolution, feature, true, fmt.Errorf("load extended Codex root observation: %w", loadErr)
		}
		if !candidateFound {
			return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
		}
		// Preserve the nearest-predecessor safety rule: a newer candidate in a
		// different group is not skipped in favour of an older candidate.
		if !requestCanUseStoredCodexGroup(c, usingGroup, candidate.Binding.SelectedGroup) {
			return resolution, feature, true, errors.New("recent Codex root channel binding is outside the current group")
		}
		resolved, resolvedFeature, strict, applyErr := applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, sourceRootID, candidate, false, cutoff)
		if applyErr != nil {
			return resolved, resolvedFeature, strict, applyErr
		}
		if raw, found := c.Get(codexPendingPassiveRootAliasContextKey); found {
			if pending, ok := raw.(codexPendingPassiveRootAlias); ok {
				pending.temporaryOnly = true
				pending.alias.Temporary = true
				c.Set(codexPendingPassiveRootAliasContextKey, pending)
			}
		}
		return resolved, resolvedFeature, strict, nil
	}
	for {
		if titleCandidate {
			candidates, loadErr := service.LoadCodexTitleRootChannelCandidates(waitContext, userID, tokenID)
			if loadErr != nil {
				if errors.Is(loadErr, context.DeadlineExceeded) && requestContext.Err() == nil &&
					time.Until(deadline) <= 0 {
					if soleCandidate != nil {
						return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, sourceRootID, *soleCandidate, true, service.CodexRequestArrival{})
					}
					return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
				}
				return resolution, feature, true, fmt.Errorf("load recent Codex root candidates: %w", loadErr)
			}
			if len(candidates) > 1 {
				return resolution, feature, true, errors.New("recent Codex root channel binding is ambiguous")
			}
			soleCandidate = nil
			if len(candidates) == 1 {
				candidate := candidates[0]
				if !requestCanUseStoredCodexGroup(c, usingGroup, candidate.Binding.SelectedGroup) {
					return resolution, feature, true, errors.New("recent Codex root channel binding is outside the current group")
				}
				soleCandidate = &candidate
			}
		} else {
			candidate, candidateFound, loadErr := service.LoadLatestCodexRootChannelObservationBefore(
				waitContext, userID, tokenID, uaRoutingOnly, cutoff,
			)
			if loadErr != nil {
				if errors.Is(loadErr, context.DeadlineExceeded) && requestContext.Err() == nil && time.Until(deadline) <= 0 {
					if allowExtendedPredecessor {
						return loadExtendedPredecessor()
					}
					return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
				}
				return resolution, feature, true, fmt.Errorf("load recent Codex root observation: %w", loadErr)
			}
			if candidateFound {
				// Check the actual nearest predecessor before checking its group. A
				// mismatched newest event must not fall through to an older group.
				if !requestCanUseStoredCodexGroup(c, usingGroup, candidate.Binding.SelectedGroup) {
					return resolution, feature, true, errors.New("recent Codex root channel binding is outside the current group")
				}
				return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, sourceRootID, candidate, false, cutoff)
			}
		}
		if err := requestContext.Err(); err != nil {
			return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", err)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if err := requestContext.Err(); err != nil {
				return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", err)
			}
			if soleCandidate == nil {
				if !allowExtendedPredecessor {
					return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
				}
				return loadExtendedPredecessor()
			}
			return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, sourceRootID, *soleCandidate, true, service.CodexRequestArrival{})
		}
		var waitErr error
		if titleCandidate {
			waitErr = waitForRecentCodexTitleRootUpdate(waitContext, userID, tokenID, remaining)
		} else {
			waitErr = waitForRecentCodexRootChannelUpdate(waitContext, userID, tokenID, uaRoutingOnly, remaining)
		}
		if waitErr != nil {
			if titleCandidate && errors.Is(waitErr, context.DeadlineExceeded) && errors.Is(waitContext.Err(), context.DeadlineExceeded) && soleCandidate != nil {
				if err := requestContext.Err(); err != nil {
					return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", err)
				}
				return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, sourceRootID, *soleCandidate, true, service.CodexRequestArrival{})
			}
			if allowExtendedPredecessor && errors.Is(waitErr, context.DeadlineExceeded) && errors.Is(waitContext.Err(), context.DeadlineExceeded) && requestContext.Err() == nil {
				return loadExtendedPredecessor()
			}
			return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", waitErr)
		}
	}
}

func applyUnlinkedCodexPassiveCandidate(
	c *gin.Context,
	resolution relaychannel.CodexRootSessionResolution,
	feature string,
	userID, tokenID int,
	sourceRootID string,
	candidate service.CodexRecentRootChannelCandidate,
	titleCandidate bool,
	cutoff service.CodexRequestArrival,
) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	c.Set(codexPendingPassiveRootAliasContextKey, codexPendingPassiveRootAlias{
		userID: userID, tokenID: tokenID, sourceRootID: sourceRootID,
		alias: service.CodexPassiveRootAlias{
			RootID: candidate.RootID, SelectedGroup: candidate.Binding.SelectedGroup,
			UARoutingOnly: candidate.Binding.UARoutingOnly, BindingFingerprint: candidate.BindingFingerprint,
		},
		claimRequired: true, titleCandidate: titleCandidate,
		observation: candidate, cutoff: cutoff,
	})
	return applyUnlinkedCodexPassiveRoot(c, resolution, candidate.RootID, feature)
}

func applyUnlinkedCodexPassiveRoot(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, rootID, feature string) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return resolution, feature, true, errors.New("Codex passive root identity is unavailable")
	}
	if !relaychannel.SetCodexPassiveRootSessionOverride(c, rootID, feature) {
		return resolution, feature, true, errors.New("invalid Codex passive root session override")
	}
	resolution.RootID = rootID
	resolution.Resolved = true
	resolution.Related = true
	return resolution, feature, true, nil
}

func commitCodexPassiveRootAlias(c *gin.Context) error {
	if c == nil {
		return nil
	}
	if c.Request != nil {
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
	}
	raw, found := c.Get(codexPendingPassiveRootAliasContextKey)
	pending, ok := raw.(codexPendingPassiveRootAlias)
	if !found || !ok {
		return nil
	}
	if !pending.claimRequired {
		return nil
	}
	if pending.titleCandidate {
		return service.ClaimCodexTitleRootAlias(c.Request.Context(), pending.userID, pending.tokenID, pending.sourceRootID, pending.alias)
	}
	return service.ClaimCodexObservedPassiveRootAlias(
		c.Request.Context(), pending.userID, pending.tokenID, pending.sourceRootID,
		pending.alias, pending.observation, pending.cutoff,
	)
}

func promoteCodexPassiveRootAlias(c *gin.Context) error {
	if c == nil {
		return nil
	}
	raw, found := c.Get(codexPendingPassiveRootAliasContextKey)
	pending, ok := raw.(codexPendingPassiveRootAlias)
	if !found || !ok {
		return nil
	}
	if pending.temporaryOnly {
		// Extended-window inference is intentionally provisional. A successful
		// request may use the alias for its current dispatch, but must not turn a
		// low-confidence predecessor into a durable 24-hour root binding.
		return nil
	}
	return service.PromoteCodexPassiveRootAlias(c.Request.Context(), pending.userID, pending.tokenID, pending.sourceRootID, pending.alias)
}

func codexPassiveRouteAuthorized(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) bool {
	if strings.TrimSpace(relaychannel.CodexPassiveRootSessionOverrideFeature(c)) != "" {
		return true
	}
	threadSource := strings.TrimSpace(resolution.ThreadSource)
	return resolution.Resolved && resolution.Related && strings.TrimSpace(resolution.RootID) != "" &&
		threadSource != "" && !strings.EqualFold(threadSource, "user")
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
	if codexPassiveAliasIsTemporary(c) {
		return codexProvisionalLineageClaim{}, nil
	}
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
	if codexPassiveAliasIsTemporary(c) {
		return
	}
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
	if codexPassiveAliasIsTemporary(c) {
		return codexProvisionalLineageClaim{}, nil
	}
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
	if codexPassiveAliasIsTemporary(c) {
		return
	}
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

func publishProvisionalCodexRootCandidates(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) error {
	if c != nil {
		if raw, found := c.Get(codexPendingPassiveRootAliasContextKey); found {
			if pending, ok := raw.(codexPendingPassiveRootAlias); ok && pending.temporaryOnly {
				// Do not publish a guessed system predecessor as a new root
				// candidate; doing so could influence later unrelated requests.
				return nil
			}
		}
	}
	userID, tokenID, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution)
	if !ok {
		return nil
	}
	if err := service.StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding); err != nil {
		return err
	}
	if err := service.StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding); err != nil {
		return err
	}
	arrival, arrivalFound, arrivalErr := currentCodexRequestArrival(c)
	if arrivalErr != nil {
		return arrivalErr
	}
	if arrivalFound {
		if err := service.StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrival); err != nil {
			return err
		}
	}
	return nil
}

func recordProvisionalCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	changed, claimed, err := claimProvisionalCodexRootChannelBinding(c, resolution, requestModel)
	if err != nil || (claimed && changed) {
		common.SysError(fmt.Sprintf("claim provisional Codex root channel binding failed: user=%d err=%v",
			common.GetContextKeyInt(c, constant.ContextKeyUserId), err))
		return
	}
	if err := publishProvisionalCodexRootCandidates(c, resolution); err != nil {
		common.SysError(fmt.Sprintf("publish provisional Codex root candidates failed: user=%d err=%v",
			common.GetContextKeyInt(c, constant.ContextKeyUserId), err))
	}
}

func recordCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	if c == nil || c.Writer == nil || c.Writer.Status() >= http.StatusBadRequest {
		return
	}
	if codexPassiveAliasIsTemporary(c) {
		return
	}
	userID, tokenID, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution)
	if !ok {
		return
	}
	if err := service.StoreCodexRootChannelBinding(userID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("store Codex root channel binding failed: user=%d channel=%d err=%v", userID, binding.ChannelID, err))
		return
	}
	// Refresh the short temporal bridge after a long first response so a title
	// generated immediately after completion still resolves to this root.
	storeRecentCodexRootChannelBinding(c, userID, tokenID, rootID, binding, "refresh")
	if err := service.StoreProvisionalCodexTitleRootChannelCandidate(userID, tokenID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("refresh title Codex root channel binding failed: user=%d token=%d channel=%d err=%v",
			userID, tokenID, binding.ChannelID, err))
	}
}

func storeRecentCodexRootChannelBinding(c *gin.Context, userID, tokenID int, rootID string, binding service.CodexRootChannelBinding, action string) {
	if err := service.StoreRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("%s recent Codex root channel binding failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, err))
	}
	arrival, arrivalFound, arrivalErr := currentCodexRequestArrival(c)
	if arrivalErr != nil {
		common.SysError(fmt.Sprintf("%s Codex root observation failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, arrivalErr))
		return
	}
	if arrivalFound {
		if err := service.StoreCodexRootChannelObservation(userID, tokenID, rootID, binding, arrival); err != nil {
			common.SysError(fmt.Sprintf("%s Codex root observation failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, err))
		}
	}
}
