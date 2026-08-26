package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// UserSessionWindowStatus is an on-demand runtime snapshot reported by a
// Codex2API upstream. Redis persistence keeps this observational status across
// NewAPI restarts; Codex2API remains the enforcement authority.
type UserSessionWindowStatus struct {
	Used          int       `json:"session_window_used"`
	Limit         int       `json:"session_window_limit"`
	WindowSeconds int       `json:"session_window_seconds"`
	UpdatedAt     time.Time `json:"session_window_updated_at"`
}

// UserSessionWindowTargetStatus keeps one Codex2API target separate from the
// aggregate shown in the user detail card. A user can be full on one target
// while another target still has capacity, so dashboard alerting must never
// decide from the summed used/limit pair.
type UserSessionWindowTargetStatus struct {
	UserID int    `json:"user_id"`
	Target string `json:"target"`
	UserSessionWindowStatus
	Full bool `json:"full"`
}

const maxUserSessionWindowEntries = 100000
const userSessionWindowCleanupInterval = time.Minute
const maxUserSessionWindowTargets = 64
const userSessionWindowRedisTimeout = 500 * time.Millisecond
const userSessionWindowRedisTTL = 31 * 24 * time.Hour
const userSessionWindowRedisRefreshInterval = time.Minute

const userSessionWindowRedisAllKey = "newapi:user-session-windows:all:v1"

var userSessionWindowRuntime = struct {
	sync.RWMutex
	items         map[int]map[string]UserSessionWindowStatus
	lastPersisted map[int]map[string]time.Time
	lastCleanup   time.Time
}{
	items:         make(map[int]map[string]UserSessionWindowStatus),
	lastPersisted: make(map[int]map[string]time.Time),
}

type persistedUserSessionWindowTarget struct {
	UserID int    `json:"user_id"`
	Target string `json:"target"`
	UserSessionWindowStatus
}

func userSessionWindowRedisDataKey(userID int) string {
	return fmt.Sprintf("newapi:user-session-windows:data:v1:%d", userID)
}

func userSessionWindowRedisField(userID int, target string) string {
	return fmt.Sprintf("%d:%x", userID, sha256.Sum256([]byte(strings.TrimSpace(target))))
}

func loadUserSessionWindowsFromRedis(userID int, now time.Time) map[string]UserSessionWindowStatus {
	if userID <= 0 || !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), userSessionWindowRedisTimeout)
	values, err := common.RDB.HGetAll(ctx, userSessionWindowRedisDataKey(userID)).Result()
	cancel()
	if err != nil {
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	items := make(map[string]UserSessionWindowStatus, len(values))
	expiredTargets := make([]string, 0)
	for target, raw := range values {
		status := UserSessionWindowStatus{}
		if common.Unmarshal([]byte(raw), &status) != nil || status.Limit <= 0 || status.Used < 0 ||
			status.WindowSeconds <= 0 || status.UpdatedAt.IsZero() ||
			now.Sub(status.UpdatedAt) > time.Duration(status.WindowSeconds)*time.Second {
			expiredTargets = append(expiredTargets, target)
			continue
		}
		items[target] = status
	}
	if len(expiredTargets) > 0 || len(items) == 0 {
		ctx, cancel = context.WithTimeout(context.Background(), userSessionWindowRedisTimeout)
		pipeline := common.RDB.TxPipeline()
		if len(expiredTargets) > 0 {
			pipeline.HDel(ctx, userSessionWindowRedisDataKey(userID), expiredTargets...)
			expiredFields := make([]string, 0, len(expiredTargets))
			for _, target := range expiredTargets {
				expiredFields = append(expiredFields, userSessionWindowRedisField(userID, target))
			}
			pipeline.HDel(ctx, userSessionWindowRedisAllKey, expiredFields...)
		}
		if len(items) == 0 {
			pipeline.Del(ctx, userSessionWindowRedisDataKey(userID))
		}
		_, _ = pipeline.Exec(ctx)
		cancel()
	}
	return items
}

func mergeUserSessionWindowsFromRedis(userID int, now time.Time) {
	items := loadUserSessionWindowsFromRedis(userID, now)
	if len(items) == 0 {
		return
	}
	userSessionWindowRuntime.Lock()
	if userSessionWindowRuntime.items[userID] == nil {
		userSessionWindowRuntime.items[userID] = make(map[string]UserSessionWindowStatus, len(items))
	}
	for target, status := range items {
		current, exists := userSessionWindowRuntime.items[userID][target]
		if !exists || status.UpdatedAt.After(current.UpdatedAt) {
			userSessionWindowRuntime.items[userID][target] = status
		}
		if userSessionWindowRuntime.lastPersisted[userID] == nil {
			userSessionWindowRuntime.lastPersisted[userID] = make(map[string]time.Time)
		}
		if status.UpdatedAt.After(userSessionWindowRuntime.lastPersisted[userID][target]) {
			userSessionWindowRuntime.lastPersisted[userID][target] = status.UpdatedAt
		}
	}
	userSessionWindowRuntime.Unlock()
}

func persistUserSessionWindowToRedis(userID int, target, evictedTarget string, status UserSessionWindowStatus) {
	if userID <= 0 || !common.RedisEnabled || common.RDB == nil {
		return
	}
	payload, err := common.Marshal(status)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), userSessionWindowRedisTimeout)
	defer cancel()
	pipeline := common.RDB.TxPipeline()
	dataKey := userSessionWindowRedisDataKey(userID)
	pipeline.HSet(ctx, dataKey, target, payload)
	globalPayload, globalErr := common.Marshal(persistedUserSessionWindowTarget{
		UserID: userID, Target: target, UserSessionWindowStatus: status,
	})
	if globalErr == nil {
		pipeline.HSet(ctx, userSessionWindowRedisAllKey, userSessionWindowRedisField(userID, target), globalPayload)
	}
	if evictedTarget != "" && evictedTarget != target {
		pipeline.HDel(ctx, dataKey, evictedTarget)
		pipeline.HDel(ctx, userSessionWindowRedisAllKey, userSessionWindowRedisField(userID, evictedTarget))
	}
	pipeline.Expire(ctx, dataKey, userSessionWindowRedisTTL)
	pipeline.Expire(ctx, userSessionWindowRedisAllKey, userSessionWindowRedisTTL)
	if _, err := pipeline.Exec(ctx); err != nil {
		common.SysError("persist user session window: " + err.Error())
	}
}

func cleanupUserSessionWindowRuntimeLocked(now time.Time) {
	for existingUserID, targets := range userSessionWindowRuntime.items {
		for existingTarget, status := range targets {
			if status.WindowSeconds <= 0 || now.Sub(status.UpdatedAt) > time.Duration(status.WindowSeconds)*time.Second {
				delete(targets, existingTarget)
				delete(userSessionWindowRuntime.lastPersisted[existingUserID], existingTarget)
			}
		}
		if len(targets) == 0 {
			delete(userSessionWindowRuntime.items, existingUserID)
			delete(userSessionWindowRuntime.lastPersisted, existingUserID)
		}
	}
	userSessionWindowRuntime.lastCleanup = now
}

func UpdateUserSessionWindowFromHeader(userID int, target string, header http.Header) {
	if userID <= 0 || header == nil {
		return
	}
	target = strings.TrimSpace(target)
	if target == "" {
		target = "default"
	}
	limit, limitErr := strconv.Atoi(strings.TrimSpace(header.Get("X-Codex2API-Session-Limit")))
	used, usedErr := strconv.Atoi(strings.TrimSpace(header.Get("X-Codex2API-Session-Used")))
	window, windowErr := strconv.Atoi(strings.TrimSpace(header.Get("X-Codex2API-Session-Window-Seconds")))
	if limitErr != nil || usedErr != nil || windowErr != nil || limit <= 0 || used < 0 || window <= 0 {
		return
	}
	now := time.Now()
	userSessionWindowRuntime.RLock()
	missingLocalState := len(userSessionWindowRuntime.items[userID]) == 0
	userSessionWindowRuntime.RUnlock()
	if missingLocalState {
		mergeUserSessionWindowsFromRedis(userID, now)
	}
	userSessionWindowRuntime.Lock()
	if now.Sub(userSessionWindowRuntime.lastCleanup) >= userSessionWindowCleanupInterval {
		cleanupUserSessionWindowRuntimeLocked(now)
	}
	if _, exists := userSessionWindowRuntime.items[userID]; !exists && len(userSessionWindowRuntime.items) >= maxUserSessionWindowEntries {
		oldestUserID := 0
		oldest := now
		for existingUserID, targets := range userSessionWindowRuntime.items {
			for _, status := range targets {
				if oldestUserID == 0 || status.UpdatedAt.Before(oldest) {
					oldestUserID = existingUserID
					oldest = status.UpdatedAt
				}
			}
		}
		if oldestUserID > 0 {
			delete(userSessionWindowRuntime.items, oldestUserID)
			delete(userSessionWindowRuntime.lastPersisted, oldestUserID)
		}
	}
	if userSessionWindowRuntime.items[userID] == nil {
		userSessionWindowRuntime.items[userID] = make(map[string]UserSessionWindowStatus)
	}
	if userSessionWindowRuntime.lastPersisted[userID] == nil {
		userSessionWindowRuntime.lastPersisted[userID] = make(map[string]time.Time)
	}
	evictedTarget := ""
	if _, exists := userSessionWindowRuntime.items[userID][target]; !exists && len(userSessionWindowRuntime.items[userID]) >= maxUserSessionWindowTargets {
		oldestTarget := ""
		oldest := now
		for existingTarget, status := range userSessionWindowRuntime.items[userID] {
			if oldestTarget == "" || status.UpdatedAt.Before(oldest) {
				oldestTarget = existingTarget
				oldest = status.UpdatedAt
			}
		}
		delete(userSessionWindowRuntime.items[userID], oldestTarget)
		delete(userSessionWindowRuntime.lastPersisted[userID], oldestTarget)
		evictedTarget = oldestTarget
	}
	previous, existed := userSessionWindowRuntime.items[userID][target]
	status := UserSessionWindowStatus{
		Used: used, Limit: limit, WindowSeconds: window, UpdatedAt: now,
	}
	userSessionWindowRuntime.items[userID][target] = status
	lastPersisted := userSessionWindowRuntime.lastPersisted[userID][target]
	shouldPersist := !existed || evictedTarget != "" || previous.Used != used || previous.Limit != limit ||
		previous.WindowSeconds != window || lastPersisted.IsZero() || now.Sub(lastPersisted) >= userSessionWindowRedisRefreshInterval
	if shouldPersist {
		userSessionWindowRuntime.lastPersisted[userID][target] = now
	}
	userSessionWindowRuntime.Unlock()
	if shouldPersist {
		persistUserSessionWindowToRedis(userID, target, evictedTarget, status)
	}
}

// ListUserSessionWindowTargetStatuses returns fresh per-target snapshots.
// onlyFull is used by the Root-only dashboard panel; keeping this filtering in
// the model avoids accidentally reintroducing the incorrect summed decision.
func ListUserSessionWindowTargetStatuses(onlyFull bool) []UserSessionWindowTargetStatus {
	now := time.Now()
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), userSessionWindowRedisTimeout)
		values, err := common.RDB.HGetAll(ctx, userSessionWindowRedisAllKey).Result()
		cancel()
		if err == nil {
			expiredFields := make([]string, 0)
			expiredTargetsByUser := make(map[int][]string)
			userSessionWindowRuntime.Lock()
			for field, raw := range values {
				item := persistedUserSessionWindowTarget{}
				decodeErr := common.Unmarshal([]byte(raw), &item)
				if decodeErr != nil || item.UserID <= 0 || strings.TrimSpace(item.Target) == "" ||
					item.Limit <= 0 || item.Used < 0 || item.WindowSeconds <= 0 || item.UpdatedAt.IsZero() ||
					now.Sub(item.UpdatedAt) > time.Duration(item.WindowSeconds)*time.Second {
					expiredFields = append(expiredFields, field)
					if decodeErr == nil && item.UserID > 0 && strings.TrimSpace(item.Target) != "" {
						expiredTargetsByUser[item.UserID] = append(expiredTargetsByUser[item.UserID], item.Target)
					}
					continue
				}
				if userSessionWindowRuntime.items[item.UserID] == nil {
					userSessionWindowRuntime.items[item.UserID] = make(map[string]UserSessionWindowStatus)
				}
				current, exists := userSessionWindowRuntime.items[item.UserID][item.Target]
				if !exists || item.UpdatedAt.After(current.UpdatedAt) {
					userSessionWindowRuntime.items[item.UserID][item.Target] = item.UserSessionWindowStatus
				}
				if userSessionWindowRuntime.lastPersisted[item.UserID] == nil {
					userSessionWindowRuntime.lastPersisted[item.UserID] = make(map[string]time.Time)
				}
				if item.UpdatedAt.After(userSessionWindowRuntime.lastPersisted[item.UserID][item.Target]) {
					userSessionWindowRuntime.lastPersisted[item.UserID][item.Target] = item.UpdatedAt
				}
			}
			userSessionWindowRuntime.Unlock()
			if len(expiredFields) > 0 {
				ctx, cancel = context.WithTimeout(context.Background(), userSessionWindowRedisTimeout)
				pipeline := common.RDB.TxPipeline()
				pipeline.HDel(ctx, userSessionWindowRedisAllKey, expiredFields...)
				for userID, targets := range expiredTargetsByUser {
					pipeline.HDel(ctx, userSessionWindowRedisDataKey(userID), targets...)
				}
				_, _ = pipeline.Exec(ctx)
				cancel()
			}
		}
	}
	userSessionWindowRuntime.Lock()
	cleanupUserSessionWindowRuntimeLocked(now)
	items := make([]UserSessionWindowTargetStatus, 0)
	for userID, targets := range userSessionWindowRuntime.items {
		for target, status := range targets {
			full := status.Limit > 0 && status.Used >= status.Limit
			if onlyFull && !full {
				continue
			}
			items = append(items, UserSessionWindowTargetStatus{
				UserID: userID, Target: target, UserSessionWindowStatus: status, Full: full,
			})
		}
	}
	userSessionWindowRuntime.Unlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].Full != items[j].Full {
			return items[i].Full
		}
		leftRatio := float64(items[i].Used) / float64(items[i].Limit)
		rightRatio := float64(items[j].Used) / float64(items[j].Limit)
		if leftRatio != rightRatio {
			return leftRatio > rightRatio
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if items[i].UserID != items[j].UserID {
			return items[i].UserID < items[j].UserID
		}
		return items[i].Target < items[j].Target
	})
	return items
}

func GetUserSessionWindowStatus(userID int) (UserSessionWindowStatus, bool) {
	userSessionWindowRuntime.RLock()
	missingLocalState := len(userSessionWindowRuntime.items[userID]) == 0
	userSessionWindowRuntime.RUnlock()
	if missingLocalState {
		mergeUserSessionWindowsFromRedis(userID, time.Now())
	}
	userSessionWindowRuntime.RLock()
	targets := userSessionWindowRuntime.items[userID]
	if len(targets) == 0 {
		userSessionWindowRuntime.RUnlock()
		return UserSessionWindowStatus{}, false
	}
	now := time.Now()
	aggregated := UserSessionWindowStatus{}
	for _, status := range targets {
		if now.Sub(status.UpdatedAt) > time.Duration(status.WindowSeconds)*time.Second {
			continue
		}
		aggregated.Used += status.Used
		aggregated.Limit += status.Limit
		if status.WindowSeconds > aggregated.WindowSeconds {
			aggregated.WindowSeconds = status.WindowSeconds
		}
		if status.UpdatedAt.After(aggregated.UpdatedAt) {
			aggregated.UpdatedAt = status.UpdatedAt
		}
	}
	userSessionWindowRuntime.RUnlock()
	if aggregated.Limit <= 0 {
		return UserSessionWindowStatus{}, false
	}
	return aggregated, true
}
