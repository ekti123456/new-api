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
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const codexRootChannelRouteContextKey = "codex_root_channel_route_v1"

const codexPassiveRootBindingWait = 1500 * time.Millisecond

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

func isPassiveCodexInternalModel(modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	modelName = strings.TrimSuffix(modelName, ratio_setting.CompactModelSuffix)
	return modelName == "gpt-5.6-luna" || modelName == "codex-auto-review"
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
	// Direct/root Luna requests remain subject to the normal published model
	// list. Only a verified derived request may use the passive-model bypass.
	if isPassiveCodexInternalModel(modelName) && !resolution.Related {
		return nil, "", false, nil
	}
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
	if !isPassiveCodexInternalModel(modelName) && !model.IsChannelEnabledForGroupModel(binding.SelectedGroup, modelName, channel.Id) {
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

// resolveUnlinkedCodexPassiveRoot associates Codex internal system/subagent
// turns with their reviewed root or, when the desktop omits the parent graph,
// the most recent root on the same user and API token. Once classified, a
// missing root binding fails closed instead of entering ordinary scheduling.
func resolveUnlinkedCodexPassiveRoot(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, modelName string) (relaychannel.CodexRootSessionResolution, string, bool, error) {
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	feature := ""
	correlationKey := ""
	reviewedRootID := ""
	classified := false
	if reviewed, guardian := relaychannel.ClassifyCodexGuardianApproval(c, modelName); guardian {
		reviewedRootID = reviewed
		feature = "guardian_approval"
		classified = true
		_, found, err := service.LoadCodexRootChannelBinding(userID, reviewedRootID)
		if err != nil {
			return resolution, feature, true, fmt.Errorf("load reviewed Codex Guardian root channel binding: %w", err)
		}
		if found {
			if !relaychannel.SetCodexPassiveRootSessionOverride(c, reviewedRootID, feature) {
				return resolution, feature, true, errors.New("invalid Codex Guardian root session override")
			}
			resolution.RootID = reviewedRootID
			resolution.Resolved = true
			resolution.Related = true
			if resolution.ThreadSource == "" {
				resolution.ThreadSource = "subagent"
			}
			if resolution.RequestKind == "" {
				resolution.RequestKind = "turn"
			}
			if resolution.SubagentKind == "" {
				resolution.SubagentKind = "guardian"
			}
			return resolution, feature, true, nil
		}
	}
	if !classified {
		feature, correlationKey, classified = relaychannel.ClassifyUnlinkedCodexPassiveInternalRequestWithCorrelation(c, resolution, modelName)
	}
	if !classified {
		return resolution, "", false, nil
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	var recent service.CodexRecentRootChannelBinding
	var found bool
	var err error
	loadRecent := func() {
		if correlationKey != "" {
			recent, found, err = service.LoadRecentCodexRootChannelBindingForCorrelation(userID, tokenID, correlationKey)
		} else {
			recent, found, err = service.LoadRecentCodexRootChannelBinding(userID, tokenID)
		}
		if err != nil || found || feature != "guardian_approval" {
			return
		}
		usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		groups := []string{usingGroup}
		if usingGroup == "auto" {
			userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
			groups = service.GetRequestAutoGroups(c, userGroup)
		}
		for _, group := range groups {
			recent, found, err = service.LoadRecentCodexUserGroupChannelBinding(userID, group)
			if err != nil || found {
				return
			}
		}
	}
	loadRecent()
	if err != nil {
		return resolution, feature, true, fmt.Errorf("load recent Codex root channel binding: %w", err)
	}
	// The desktop can dispatch the first title at almost the same instant as
	// the main turn. Wait briefly for the main distributor to finish selecting
	// its channel instead of letting scheduler ordering make naming flaky.
	if !found && correlationKey != "" && c.Request != nil {
		timer := time.NewTimer(codexPassiveRootBindingWait)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer timer.Stop()
		defer ticker.Stop()
		for !found {
			select {
			case <-c.Request.Context().Done():
				return resolution, feature, true, c.Request.Context().Err()
			case <-timer.C:
				return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
			case <-ticker.C:
				loadRecent()
				if err != nil {
					return resolution, feature, true, fmt.Errorf("load recent Codex root channel binding: %w", err)
				}
			}
		}
	}
	if !found {
		return resolution, feature, true, errors.New("recent Codex root channel binding is unavailable")
	}
	rootID := strings.TrimSpace(recent.RootID)
	if rootID == "" && feature == "guardian_approval" {
		rootID = reviewedRootID
	}
	if rootID == "" {
		return resolution, feature, true, errors.New("recent Codex root identity is unavailable")
	}
	// A rootless main turn can still publish a short user/group-scoped channel
	// bridge. Once the tightly classified Guardian supplies its reviewed root,
	// materialize a provisional exact binding so all normal validation remains
	// centralized in prepareCodexRootChannelRoute.
	if err := service.StoreProvisionalCodexRootChannelBinding(userID, rootID, recent.Binding); err != nil {
		return resolution, feature, true, fmt.Errorf("store recovered Codex root channel binding: %w", err)
	}
	if !relaychannel.SetCodexPassiveRootSessionOverride(c, rootID, feature) {
		return resolution, feature, true, errors.New("invalid Codex passive root session override")
	}
	resolution.RootID = rootID
	resolution.Resolved = true
	resolution.Related = true
	if feature == "guardian_approval" {
		if resolution.ThreadSource == "" {
			resolution.ThreadSource = "subagent"
		}
		if resolution.RequestKind == "" {
			resolution.RequestKind = "turn"
		}
		if resolution.SubagentKind == "" {
			resolution.SubagentKind = "guardian"
		}
	}
	return resolution, feature, true, nil
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

func isCodexRecentMainRoute(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) bool {
	if c == nil || resolution.Related || isPassiveCodexInternalModel(requestModel) {
		return false
	}
	threadSource := strings.ToLower(strings.TrimSpace(resolution.ThreadSource))
	if threadSource == "system" || threadSource == "subagent" {
		return false
	}
	// Project-level ambient jobs are independent background threads, not
	// user-visible roots. Publishing one as the recent root could make a later
	// title/summary generation borrow the background job's channel and key.
	if _, bypass := relaychannel.ClassifyCodexSessionAccountingBypass(c, resolution, requestModel); bypass {
		return false
	}
	return true
}

func selectedCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) (int, int, string, service.CodexRootChannelBinding, bool) {
	if !resolution.Resolved || strings.TrimSpace(resolution.RootID) == "" || !isCodexRecentMainRoute(c, resolution, requestModel) {
		return 0, 0, "", service.CodexRootChannelBinding{}, false
	}
	userID, tokenID, binding, ok := selectedCodexChannelBinding(c)
	if !ok {
		return 0, 0, "", service.CodexRootChannelBinding{}, false
	}
	return userID, tokenID, resolution.RootID, binding, true
}

func recordProvisionalCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	mainRoute := isCodexRecentMainRoute(c, resolution, requestModel)
	userID, _, recentBinding, recentOK, bindingReason := inspectSelectedCodexChannelBinding(c)
	if mainRoute && !recentOK {
		logSkippedCodexRootBridge(c, resolution, requestModel, bindingReason)
	}
	if recentOK && mainRoute {
		rootID := ""
		if resolution.Resolved {
			rootID = resolution.RootID
		}
		storeRecentCodexUserGroupChannelBinding(userID, rootID, recentBinding, "store")
	}
	userID, tokenID, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution, requestModel)
	if !ok {
		return
	}
	if err := service.StoreProvisionalCodexRootChannelBinding(userID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("store provisional Codex root channel binding failed: user=%d channel=%d err=%v", userID, binding.ChannelID, err))
		return
	}
	storeRecentCodexRootChannelBindings(c, userID, tokenID, rootID, binding, "store")
}

func recordCodexRootChannelBinding(c *gin.Context, resolution relaychannel.CodexRootSessionResolution, requestModel string) {
	if c == nil || c.Writer == nil || c.Writer.Status() >= http.StatusBadRequest {
		return
	}
	userID, _, recentBinding, recentOK := selectedCodexChannelBinding(c)
	if recentOK && isCodexRecentMainRoute(c, resolution, requestModel) {
		rootID := ""
		if resolution.Resolved {
			rootID = resolution.RootID
		}
		storeRecentCodexUserGroupChannelBinding(userID, rootID, recentBinding, "refresh")
	}
	userID, tokenID, rootID, binding, ok := selectedCodexRootChannelBinding(c, resolution, requestModel)
	if !ok {
		return
	}
	if err := service.StoreCodexRootChannelBinding(userID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("store Codex root channel binding failed: user=%d channel=%d err=%v", userID, binding.ChannelID, err))
		return
	}
	// Refresh the short temporal bridge after a long first response so a title
	// generated immediately after completion still resolves to this root.
	storeRecentCodexRootChannelBindings(c, userID, tokenID, rootID, binding, "refresh")
}

func storeRecentCodexRootChannelBindings(c *gin.Context, userID, tokenID int, rootID string, binding service.CodexRootChannelBinding, action string) {
	if err := service.StoreRecentCodexRootChannelBinding(userID, tokenID, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("%s recent Codex root channel binding failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, err))
	}
	correlationKey := relaychannel.CodexRootPromptCorrelationKey(c)
	if correlationKey == "" {
		return
	}
	if err := service.StoreRecentCodexRootChannelBindingForCorrelation(userID, tokenID, correlationKey, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("%s correlated Codex root channel binding failed: user=%d token=%d channel=%d err=%v", action, userID, tokenID, binding.ChannelID, err))
	}
}

func storeRecentCodexUserGroupChannelBinding(userID int, rootID string, binding service.CodexRootChannelBinding, action string) {
	if err := service.StoreRecentCodexUserGroupChannelBinding(userID, binding.SelectedGroup, rootID, binding); err != nil {
		common.SysError(fmt.Sprintf("%s recent Codex user/group channel binding failed: user=%d group=%s channel=%d err=%v", action, userID, binding.SelectedGroup, binding.ChannelID, err))
	}
}
