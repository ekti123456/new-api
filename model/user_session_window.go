package model

import (
	"net/http"
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

const maxUserSessionWindowEntries = 100000
const userSessionWindowCleanupInterval = time.Minute
const maxUserSessionWindowTargets = 64

var userSessionWindowRuntime = struct {
	sync.RWMutex
	items       map[int]map[string]UserSessionWindowStatus
	lastCleanup time.Time
}{items: make(map[int]map[string]UserSessionWindowStatus)}

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
		for existingUserID, targets := range userSessionWindowRuntime.items {
			for existingTarget, status := range targets {
				if now.Sub(status.UpdatedAt) > time.Duration(status.WindowSeconds)*time.Second {
					delete(targets, existingTarget)
				}
			}
			if len(targets) == 0 {
				delete(userSessionWindowRuntime.items, existingUserID)
			}
		}
		userSessionWindowRuntime.lastCleanup = now
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
