package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const codexRootChannelRouteContextKey = "codex_root_channel_route_v1"

type codexRootChannelRoute struct {
	binding service.CodexRootChannelBinding
	key     string
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

func prepareCodexRootChannelRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, modelName, usingGroup string) (*model.Channel, string, bool, error) {
	if c == nil || !resolution.Resolved || strings.TrimSpace(resolution.RootID) == "" {
		return nil, "", false, nil
	}
	if isIndependentCodexInternalRoot(resolution) {
		// Independent background roots are ordinary scheduling units. Ignoring
		// any legacy binding for the same ID also makes the behavior effective
		// immediately after upgrading from the old over-broad bridge logic.
		return nil, "", false, nil
	}
	passiveInternal := codexPassiveRouteAuthorized(c, resolution)
	binding, found, err := service.LoadCodexRootChannelBinding(c.GetInt(string(constant.ContextKeyUserId)), resolution.RootID)
	if err != nil {
		return nil, "", false, fmt.Errorf("load root channel binding: %w", err)
	}
	if !found {
		return nil, "", false, nil
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
// user's most recent root through the short-lived user/token bridge. Other
// independent internal roots use ordinary scheduling and never borrow that
// bridge, which prevents concurrent Codex windows from being cross-bound.
func resolveUnlinkedCodexPassiveRoot(c *gin.Context, resolution relaychannel.CodexRootSessionResolution) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if feature, linked := relaychannel.ClassifyLinkedCodexPassiveInternalRequest(resolution); linked {
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
	recent, found, err := service.LoadRecentCodexRootChannelBinding(userID, tokenID)
	if err != nil {
		return resolution, feature, true, fmt.Errorf("load recent Codex root channel binding: %w", err)
	}
	if !found {
		return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
	}
	rootID := strings.TrimSpace(recent.RootID)
	if rootID == "" {
		return resolution, feature, true, errors.New("recent Codex root identity is unavailable")
	}
	// Materialize the short bridge as a provisional exact binding so all normal
	// channel/key validation remains centralized in prepareCodexRootChannelRoute.
	if err := service.StoreProvisionalCodexRootChannelBinding(userID, rootID, recent.Binding); err != nil {
		return resolution, feature, true, fmt.Errorf("store recovered Codex root channel binding: %w", err)
	}
	if !relaychannel.SetCodexPassiveRootSessionOverride(c, rootID, feature) {
		return resolution, feature, true, errors.New("invalid Codex passive root session override")
	}
	resolution.RootID = rootID
	resolution.Resolved = true
	resolution.Related = true
	return resolution, feature, true, nil
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

func recordProvisionalCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	mainRoute := isCodexRecentMainRoute(c, resolution)
	_, _, _, recentOK, bindingReason := inspectSelectedCodexChannelBinding(c)
	if mainRoute && !recentOK {
		logSkippedCodexRootBridge(c, resolution, requestModel, bindingReason)
	}
	userID, tokenID, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution)
	if !ok {
		return
	}
	if err := service.StoreProvisionalCodexRootChannelBinding(userID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("store provisional Codex root channel binding failed: user=%d channel=%d err=%v", userID, binding.ChannelID, err))
		return
	}
	storeRecentCodexRootChannelBinding(userID, tokenID, rootID, binding, "store")
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
	if err := service.StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("%s recent Codex root channel binding failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, err))
	}
}
