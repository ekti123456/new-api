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
)

var (
	codexUnlinkedPassiveRootWaitTimeout  = 3 * time.Second
	codexLinkedNamingRootWaitTimeout     = 3 * time.Second
	waitForRecentCodexRootChannelUpdate  = service.WaitForRecentCodexRootChannelUpdate
	waitForCodexRootChannelBindingUpdate = service.WaitForCodexRootChannelBindingUpdate
)

type codexRootChannelRoute struct {
	binding service.CodexRootChannelBinding
	key     string
}

type codexPendingPassiveRootAlias struct {
	userID        int
	tokenID       int
	systemRootID  string
	alias         service.CodexPassiveRootAlias
	claimRequired bool
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

func loadLinkedCodexNamingRootBinding(c *gin.Context, userID int, rootID string) (service.CodexRootChannelBinding, bool, error) {
	rootID = strings.TrimSpace(rootID)
	if c == nil || userID <= 0 || rootID == "" {
		return service.CodexRootChannelBinding{}, false, nil
	}
	requestContext := context.Background()
	if c.Request != nil {
		requestContext = c.Request.Context()
	}
	binding, found, err := service.LoadCodexRootChannelBindingContext(requestContext, userID, rootID)
	if err != nil || found || codexLinkedNamingRootWaitTimeout <= 0 {
		return binding, found, err
	}

	waitContext, cancelWait := context.WithTimeout(requestContext, codexLinkedNamingRootWaitTimeout)
	defer cancelWait()
	for {
		waitErr := waitForCodexRootChannelBindingUpdate(waitContext, userID, rootID, codexLinkedNamingRootWaitTimeout)
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) && requestContext.Err() == nil {
				return service.CodexRootChannelBinding{}, false, nil
			}
			return service.CodexRootChannelBinding{}, false, fmt.Errorf("wait for linked Codex naming root binding: %w", waitErr)
		}
		binding, found, err = service.LoadCodexRootChannelBindingContext(waitContext, userID, rootID)
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

func prepareCodexRootChannelRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, modelName, usingGroup string) (*model.Channel, string, bool, error) {
	if c == nil {
		return nil, "", false, nil
	}
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
	} else {
		binding, found, err = service.LoadCodexRootChannelBindingForRoutingSide(userID, resolution.RootID, requestUARoutingOnly)
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("load root channel binding: %w", err)
	}
	if !found {
		return nil, "", false, nil
	}
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

func isIndependentCodexInternalRoot(resolution relaychannel.CodexRootSessionResolution) bool {
	threadSource := strings.TrimSpace(resolution.ThreadSource)
	return resolution.Resolved && !resolution.Related && strings.TrimSpace(resolution.RootID) != "" &&
		threadSource != "" && !strings.EqualFold(threadSource, "user") && !strings.EqualFold(threadSource, "system")
}

// resolveUnlinkedCodexPassiveRoot pins an explicitly related child to its exact
// root. The independent system thread used for project metadata may recover the
// user's root through an immutable system-session alias or a sole active
// user/token/UA-side candidate. Other independent internal roots use ordinary
// scheduling and never borrow that bridge.
func resolveUnlinkedCodexPassiveRoot(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if feature, linked := relaychannel.ClassifyLinkedCodexPassiveInternalRequest(resolution); linked {
		// A coherent explicit parent graph is intentionally scoped to the NewAPI
		// user and root, not to one API key. This lets the same user resume a
		// Codex task after rotating keys; prepareCodexRootChannelRoute still
		// rechecks the current group, UA side and any token-specific channel.
		if !relaychannel.SetCodexPassiveRootSessionOverride(c, resolution.RootID, feature) {
			return resolution, feature, true, errors.New("invalid linked Codex passive root session override")
		}
		return resolution, feature, true, nil
	}
	feature, classified := relaychannel.ClassifyUnlinkedCodexSystemRequest(resolution)
	if !classified {
		return resolution, "", false, nil
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	systemRootID := strings.TrimSpace(resolution.RootID)
	uaRoutingOnly := common.GetContextKeyBool(c, constant.ContextKeyChannelAffinityUserAgentRouted)
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	requestContext := c.Request.Context()
	alias, found, err := service.LoadCodexPassiveRootAlias(requestContext, userID, tokenID, systemRootID)
	if err != nil {
		return resolution, feature, true, fmt.Errorf("load Codex passive root alias: %w", err)
	}
	if found {
		if alias.UARoutingOnly != uaRoutingOnly || !requestCanUseStoredCodexGroup(c, usingGroup, alias.SelectedGroup) {
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
			userID: userID, tokenID: tokenID, systemRootID: systemRootID, alias: alias,
		})
		return applyUnlinkedCodexPassiveRoot(c, resolution, alias.RootID, feature)
	}

	deadline := time.Now().Add(codexUnlinkedPassiveRootWaitTimeout)
	waitContext, cancelWait := context.WithTimeout(requestContext, codexUnlinkedPassiveRootWaitTimeout)
	defer cancelWait()
	var soleCandidate *service.CodexRecentRootChannelCandidate
	for {
		candidates, loadErr := service.LoadRecentCodexRootChannelCandidates(waitContext, userID, tokenID, uaRoutingOnly)
		if loadErr != nil {
			if errors.Is(loadErr, context.DeadlineExceeded) && requestContext.Err() == nil &&
				time.Until(deadline) <= 0 && soleCandidate != nil {
				return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, systemRootID, *soleCandidate)
			}
			return resolution, feature, true, fmt.Errorf("load recent Codex root candidates: %w", loadErr)
		}
		if err := requestContext.Err(); err != nil {
			return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", err)
		}
		// Ambiguity is evaluated across the complete user/token/UA-side scope.
		// Filtering by the requested group first could incorrectly pick one root
		// while the atomic alias claim still sees multiple active candidates.
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
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if err := requestContext.Err(); err != nil {
				return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", err)
			}
			if soleCandidate == nil {
				return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
			}
			return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, systemRootID, *soleCandidate)
		}
		if waitErr := waitForRecentCodexRootChannelUpdate(waitContext, userID, tokenID, uaRoutingOnly, remaining); waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) && errors.Is(waitContext.Err(), context.DeadlineExceeded) && soleCandidate != nil {
				if err := requestContext.Err(); err != nil {
					return resolution, feature, true, fmt.Errorf("wait for recent Codex root channel binding: %w", err)
				}
				return applyUnlinkedCodexPassiveCandidate(c, resolution, feature, userID, tokenID, systemRootID, *soleCandidate)
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
	systemRootID string,
	candidate service.CodexRecentRootChannelCandidate,
) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	c.Set(codexPendingPassiveRootAliasContextKey, codexPendingPassiveRootAlias{
		userID: userID, tokenID: tokenID, systemRootID: systemRootID,
		alias: service.CodexPassiveRootAlias{
			RootID: candidate.RootID, SelectedGroup: candidate.Binding.SelectedGroup,
			UARoutingOnly: candidate.Binding.UARoutingOnly, BindingFingerprint: candidate.BindingFingerprint,
		},
		claimRequired: true,
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
	return service.ClaimCodexPassiveRootAlias(c.Request.Context(), pending.userID, pending.tokenID, pending.systemRootID, pending.alias)
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
	return service.PromoteCodexPassiveRootAlias(c.Request.Context(), pending.userID, pending.tokenID, pending.systemRootID, pending.alias)
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

func claimProvisionalCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) (bool, bool, error) {
	mainRoute := isCodexRecentMainRoute(c, resolution)
	_, _, _, recentOK, bindingReason := inspectSelectedCodexChannelBinding(c)
	if mainRoute && !recentOK {
		logSkippedCodexRootBridge(c, resolution, requestModel, bindingReason)
	}
	userID, tokenID, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution)
	if !ok {
		return false, false, nil
	}
	winner, selectedWon, err := service.ClaimProvisionalCodexRootChannelBinding(userID, rootID, binding)
	if err != nil {
		return false, true, err
	}
	if !selectedWon {
		// The request will be aborted before upstream dispatch. Do not publish
		// another token's/root contender's winning binding into this token's
		// recent-candidate scope.
		return true, true, nil
	}
	if err := service.StoreProvisionalRecentCodexRootChannelCandidate(userID, tokenID, rootID, winner); err != nil {
		return false, true, err
	}
	return false, true, nil
}

func recordProvisionalCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	if _, _, err := claimProvisionalCodexRootChannelBinding(c, resolution, requestModel); err != nil {
		common.SysError(fmt.Sprintf("claim provisional Codex root channel binding failed: user=%d err=%v",
			common.GetContextKeyInt(c, constant.ContextKeyUserId), err))
	}
}

func recordCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	if c == nil || c.Writer == nil || c.Writer.Status() >= http.StatusBadRequest {
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
	storeRecentCodexRootChannelBinding(userID, tokenID, rootID, binding, "refresh")
}

func storeRecentCodexRootChannelBinding(userID, tokenID int, rootID string, binding service.CodexRootChannelBinding, action string) {
	if err := service.StoreRecentCodexRootChannelCandidate(userID, tokenID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("%s recent Codex root channel binding failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, err))
	}
}
