package perfmetrics

import (
	"sync"
	"time"
)

const userErrorRateEligibilityGrace = 24 * time.Hour

var userErrorRateEligibility = struct {
	sync.RWMutex
	deadlines map[int]int64
}{deadlines: make(map[int]int64)}

var userErrorRateEligibilityRefreshMutex sync.Mutex

// refreshUserErrorRateEligibility admits every user currently shown by the
// performance-anomaly calculation. Repeated refreshes extend the deadline, so
// a user remains admitted for 24 hours after their last appearance.
func refreshUserErrorRateEligibility(items []UserAnomalyItem) {
	userErrorRateEligibilityRefreshMutex.Lock()
	defer userErrorRateEligibilityRefreshMutex.Unlock()

	now := userErrorRateNow().Unix()
	deadline := now + int64(userErrorRateEligibilityGrace/time.Second)
	seen := make(map[int]struct{}, len(items))
	expiredUserIDs := make([]int, 0)

	userErrorRateEligibility.Lock()
	for userID, existingDeadline := range userErrorRateEligibility.deadlines {
		if existingDeadline <= now {
			delete(userErrorRateEligibility.deadlines, userID)
			expiredUserIDs = append(expiredUserIDs, userID)
		}
	}
	userErrorRateEligibility.Unlock()
	for _, userID := range expiredUserIDs {
		clearUserErrorRateSamples(userID)
	}

	userErrorRateEligibility.Lock()
	for _, item := range items {
		if item.UserID <= 0 {
			continue
		}
		if _, exists := seen[item.UserID]; exists {
			continue
		}
		seen[item.UserID] = struct{}{}
		userErrorRateEligibility.deadlines[item.UserID] = deadline
	}
	userErrorRateEligibility.Unlock()
}

func getUserErrorRateEligibilityDeadline(userID int) (int64, bool) {
	if userID <= 0 {
		return 0, false
	}
	now := userErrorRateNow().Unix()
	userErrorRateEligibility.RLock()
	deadline, exists := userErrorRateEligibility.deadlines[userID]
	userErrorRateEligibility.RUnlock()
	if !exists || deadline <= now {
		if exists {
			userErrorRateEligibilityRefreshMutex.Lock()
			userErrorRateEligibility.Lock()
			currentDeadline, stillExists := userErrorRateEligibility.deadlines[userID]
			renewed := stillExists && currentDeadline > now
			if stillExists && currentDeadline <= now {
				delete(userErrorRateEligibility.deadlines, userID)
			}
			userErrorRateEligibility.Unlock()
			if stillExists && currentDeadline <= now {
				clearUserErrorRateSamples(userID)
			}
			userErrorRateEligibilityRefreshMutex.Unlock()
			if renewed {
				return currentDeadline, true
			}
		}
		return 0, false
	}
	return deadline, true
}
