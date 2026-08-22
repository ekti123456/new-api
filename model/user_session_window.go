package model

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UserSessionWindowStatus is an on-demand runtime snapshot reported by a
// Codex2API upstream. It is intentionally not persisted and never participates
// in NewAPI authorization; Codex2API remains the enforcement authority.
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

var userSessionWindowRuntime = struct {
	sync.RWMutex
	items       map[int]map[string]UserSessionWindowStatus
	lastCleanup time.Time
}{items: make(map[int]map[string]UserSessionWindowStatus)}

func cleanupUserSessionWindowRuntimeLocked(now time.Time) {
	for existingUserID, targets := range userSessionWindowRuntime.items {
		for existingTarget, status := range targets {
			if status.WindowSeconds <= 0 || now.Sub(status.UpdatedAt) > time.Duration(status.WindowSeconds)*time.Second {
				delete(targets, existingTarget)
			}
		}
		if len(targets) == 0 {
			delete(userSessionWindowRuntime.items, existingUserID)
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
		}
	}
	if userSessionWindowRuntime.items[userID] == nil {
		userSessionWindowRuntime.items[userID] = make(map[string]UserSessionWindowStatus)
	}
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
	}
	userSessionWindowRuntime.items[userID][target] = UserSessionWindowStatus{
		Used: used, Limit: limit, WindowSeconds: window, UpdatedAt: now,
	}
	userSessionWindowRuntime.Unlock()
}

// ListUserSessionWindowTargetStatuses returns fresh per-target snapshots.
// onlyFull is used by the Root-only dashboard panel; keeping this filtering in
// the model avoids accidentally reintroducing the incorrect summed decision.
func ListUserSessionWindowTargetStatuses(onlyFull bool) []UserSessionWindowTargetStatus {
	now := time.Now()
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
