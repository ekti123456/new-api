package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func abortCodexBackgroundRootFailure(requestContext *gin.Context, resolution relaychannel.CodexRootSessionResolution, failure error) bool {
	if !relaychannel.CodexRequestNeedsRootAccountWait(resolution.ThreadSource) {
		return false
	}
	message := "未找到身份范围及会话前缀匹配的有效主会话，请先发起主请求。"
	if errors.Is(failure, errCodexBackgroundRootAmbiguous) || errors.Is(failure, service.ErrCodexPrefixRootAmbiguous) {
		message = "同一身份范围及会话前缀对应多个主会话，无法确定后台请求归属。"
	} else if errors.Is(failure, service.ErrCodexSessionPrefixUnavailable) {
		message = service.ErrCodexSessionPrefixUnavailable.Error()
	} else if errors.Is(failure, context.DeadlineExceeded) {
		message = "等待主会话绑定超时，请先发起主请求。"
	}
	abortWithOpenAiMessage(requestContext, http.StatusBadRequest, message, types.ErrorCode("codex_background_root_unavailable"))
	return true
}

func resolveCodexPassiveThreadBinding(requestContext *gin.Context, resolution relaychannel.CodexRootSessionResolution, wait bool) (relaychannel.CodexRootSessionResolution, bool, error) {
	if !resolution.Resolved || !relaychannel.CodexRequestNeedsRootAccountWait(resolution.ThreadSource) || strings.TrimSpace(resolution.ForkedFromID) != "" {
		return resolution, false, nil
	}
	userID := common.GetContextKeyInt(requestContext, constant.ContextKeyUserId)
	parentID := strings.TrimSpace(resolution.RootID)
	if userID <= 0 || parentID == "" {
		return resolution, false, errors.New("后台请求缺少有效的用户或父根信息")
	}
	lookupContext := requestContext.Request.Context()
	if wait {
		var cancel context.CancelFunc
		lookupContext, cancel = context.WithTimeout(lookupContext, codexRootRouteWaitTimeout(requestContext, codexLinkedRootWaitTimeout))
		defer cancel()
	}
	for {
		mapping, _, found, err := service.ResolveCodexThreadRootBinding(lookupContext, userID, parentID)
		if err != nil {
			return resolution, false, err
		}
		if found && !resolution.Related && (!mapping.RootOwner || !strings.EqualFold(mapping.RootID, parentID)) {
			found = false
		}
		basis := "thread_binding"
		rootID := mapping.RootID
		if !found && resolution.Related {
			_, found, err = loadUniqueCodexRootChannelBindingContext(lookupContext, userID, parentID)
			rootID = parentID
			basis = "explicit_parent"
			if err != nil {
				return resolution, false, err
			}
		}
		if found {
			feature := "related_internal"
			if strings.EqualFold(resolution.ThreadSource, "system") {
				feature = "system_passive"
			}
			relaychannel.RecordCodexRootAssociation(requestContext, parentID, basis, 1)
			resolved, _, _, applyErr := applyUnlinkedCodexPassiveRoot(requestContext, resolution, rootID, feature)
			return resolved, true, applyErr
		}
		if !wait {
			return resolution, false, nil
		}
		if err := lookupContext.Err(); err != nil {
			return resolution, false, err
		}
		if err := waitForCodexRootChannelBindingUpdate(lookupContext, userID, parentID, time.Second); err != nil {
			return resolution, false, err
		}
	}
}
