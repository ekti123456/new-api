package service

import (
	"context"
	"time"

	"github.com/samber/hot"
	"golang.org/x/sync/singleflight"
)

var codexPassiveCandidateLookups singleflight.Group
var codexEmptyPassiveCandidateScopes = hot.NewHotCache[string, bool](hot.LRU, 4096).WithTTL(time.Second).Build()

func LoadCodexPassiveRootCandidates(ctx context.Context, userID, tokenID int, title bool, scope CodexPassiveRootScope) ([]CodexRecentRootChannelCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := "recent:" + codexPassiveRootScopeKey(userID, tokenID, scope)
	if title {
		key = "title:" + codexPassiveRootScopeKey(userID, tokenID, scope)
	}
	if _, found, _ := codexEmptyPassiveCandidateScopes.Get(key); found {
		return nil, nil
	}
	result := codexPassiveCandidateLookups.DoChan(key, func() (any, error) {
		lookupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), codexPassiveRootRedisTimeout)
		defer cancel()
		var candidates []CodexRecentRootChannelCandidate
		var err error
		if title {
			candidates, err = LoadCodexTitleRootChannelCandidates(lookupContext, userID, tokenID, scope)
		} else {
			for _, side := range []bool{false, true} {
				var sideCandidates []CodexRecentRootChannelCandidate
				sideCandidates, err = LoadRecentCodexRootChannelCandidates(lookupContext, userID, tokenID, side, scope)
				if err != nil {
					break
				}
				candidates = append(candidates, sideCandidates...)
			}
		}
		if err == nil && len(candidates) == 0 {
			codexEmptyPassiveCandidateScopes.Set(key, true)
		}
		return candidates, err
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case loaded := <-result:
		if loaded.Err != nil {
			return nil, loaded.Err
		}
		return loaded.Val.([]CodexRecentRootChannelCandidate), nil
	}
}
