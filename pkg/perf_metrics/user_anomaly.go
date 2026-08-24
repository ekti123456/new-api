package perfmetrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"

	"github.com/gin-gonic/gin"
)

const (
	userMetricTtftBucketMs        int64 = 100
	userMetricRollupBucketSeconds int64 = 300
	userMetricEarlyFlushRequests  int64 = 50000
	defaultUserAnomalyPageSize          = 20
	maxUserAnomalyPageSize              = 100
)

type userMetricBufferKey struct {
	userID   int
	group    string
	bucketTs int64
}

type userMetricIdentityKey struct {
	userID int
	group  string
}

type bufferedUserMetricRollup struct {
	sample    model.PerfUserSample
	histogram map[int64]int64
}

var userMetricSampleBuffer = struct {
	sync.Mutex
	rollups      map[userMetricBufferKey]*bufferedUserMetricRollup
	requestCount int64
}{
	rollups: make(map[userMetricBufferKey]*bufferedUserMetricRollup),
}

var userMetricFlushMutex sync.Mutex

func CaptureUserMetricIdentity(c *gin.Context, info *relaycommon.RelayInfo) *UserMetricIdentity {
	if c == nil || info == nil || info.UserId <= 0 {
		return nil
	}
	identity := &UserMetricIdentity{
		UserID:   info.UserId,
		Username: c.GetString("username"),
	}
	if common.RequestIPLogEnabled {
		identity.IP = c.ClientIP()
	}
	if c.Request != nil {
		identity.AccessURL = common.GetRequestOrigin(c.Request)
	}
	return identity
}

// IsRelaySampleSuccess intentionally mirrors the visible stream-status badge
// in usage logs. Local routing/upstream errors without a consume-log stream
// status are not counted as user performance errors.
func IsRelaySampleSuccess(info *relaycommon.RelayInfo) bool {
	if info == nil || !info.IsStream || info.StreamStatus == nil {
		return true
	}
	return info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors()
}

func bufferUserMetricSample(sample Sample) {
	if sample.User == nil || sample.User.UserID <= 0 || !perf_metrics_setting.IsUserAnomalyGroupMonitored(sample.Group) {
		return
	}

	now := time.Now().Unix()
	bucketTs := now - now%userMetricRollupBucketSeconds
	key := userMetricBufferKey{userID: sample.User.UserID, group: sample.Group, bucketTs: bucketTs}
	shouldFlush := false

	userMetricSampleBuffer.Lock()
	rollup := userMetricSampleBuffer.rollups[key]
	if rollup == nil {
		rollup = &bufferedUserMetricRollup{
			sample: model.PerfUserSample{
				UserId:     sample.User.UserID,
				Username:   sample.User.Username,
				Group:      sample.Group,
				CreatedAt:  bucketTs,
				LastSeenAt: now,
				Ip:         sample.User.IP,
				AccessUrl:  sample.User.AccessURL,
			},
			histogram: make(map[int64]int64),
		}
		userMetricSampleBuffer.rollups[key] = rollup
	}
	rollup.sample.RequestCount++
	rollup.sample.LastSeenAt = now
	if sample.User.Username != "" {
		rollup.sample.Username = sample.User.Username
	}
	// Keep the first non-empty snapshot. IP and access URL are observation
	// metadata, not live lookups performed every time the dashboard refreshes.
	if rollup.sample.Ip == "" && sample.User.IP != "" {
		rollup.sample.Ip = sample.User.IP
	}
	if rollup.sample.AccessUrl == "" && sample.User.AccessURL != "" {
		rollup.sample.AccessUrl = sample.User.AccessURL
	}
	if sample.UserError {
		rollup.sample.ErrorCount++
	}
	if sample.HasTtft && sample.TtftMs > 0 {
		rollup.sample.TtftCount++
		rollup.sample.TtftSumMs += sample.TtftMs
		rollup.histogram[sample.TtftMs/userMetricTtftBucketMs]++
	}
	userMetricSampleBuffer.requestCount++
	shouldFlush = userMetricSampleBuffer.requestCount >= userMetricEarlyFlushRequests
	userMetricSampleBuffer.Unlock()

	if shouldFlush {
		if err := flushUserMetricSamples(); err != nil {
			common.SysError("failed to early flush user performance samples: " + err.Error())
		}
	}
}

func takeUserMetricRollups() map[userMetricBufferKey]*bufferedUserMetricRollup {
	userMetricSampleBuffer.Lock()
	defer userMetricSampleBuffer.Unlock()
	if len(userMetricSampleBuffer.rollups) == 0 {
		return nil
	}
	rollups := userMetricSampleBuffer.rollups
	userMetricSampleBuffer.rollups = make(map[userMetricBufferKey]*bufferedUserMetricRollup)
	userMetricSampleBuffer.requestCount = 0
	return rollups
}

func mergeBufferedUserMetricRollup(target *bufferedUserMetricRollup, source *bufferedUserMetricRollup) {
	if target == nil || source == nil {
		return
	}
	if target.sample.CreatedAt == 0 || (source.sample.CreatedAt > 0 && source.sample.CreatedAt < target.sample.CreatedAt) {
		target.sample.CreatedAt = source.sample.CreatedAt
		if source.sample.Ip != "" {
			target.sample.Ip = source.sample.Ip
		}
		if source.sample.AccessUrl != "" {
			target.sample.AccessUrl = source.sample.AccessUrl
		}
	}
	if source.sample.LastSeenAt >= target.sample.LastSeenAt {
		target.sample.LastSeenAt = source.sample.LastSeenAt
		if source.sample.Username != "" {
			target.sample.Username = source.sample.Username
		}
	}
	if target.sample.Ip == "" {
		target.sample.Ip = source.sample.Ip
	}
	if target.sample.AccessUrl == "" {
		target.sample.AccessUrl = source.sample.AccessUrl
	}
	target.sample.RequestCount += source.sample.RequestCount
	target.sample.ErrorCount += source.sample.ErrorCount
	target.sample.TtftCount += source.sample.TtftCount
	target.sample.TtftSumMs += source.sample.TtftSumMs
	for bucket, count := range source.histogram {
		target.histogram[bucket] += count
	}
}

func restoreUserMetricRollups(rollups map[userMetricBufferKey]*bufferedUserMetricRollup) {
	userMetricSampleBuffer.Lock()
	defer userMetricSampleBuffer.Unlock()
	for key, source := range rollups {
		target := userMetricSampleBuffer.rollups[key]
		if target == nil {
			userMetricSampleBuffer.rollups[key] = source
			userMetricSampleBuffer.requestCount += source.sample.RequestCount
			continue
		}
		mergeBufferedUserMetricRollup(target, source)
		userMetricSampleBuffer.requestCount += source.sample.RequestCount
	}
}

func flushUserMetricSamples() error {
	userMetricFlushMutex.Lock()
	defer userMetricFlushMutex.Unlock()

	rollups := takeUserMetricRollups()
	if len(rollups) == 0 {
		return nil
	}
	samples := make([]model.PerfUserSample, 0, len(rollups))
	for _, rollup := range rollups {
		histogram, err := common.Marshal(rollup.histogram)
		if err != nil {
			restoreUserMetricRollups(rollups)
			return err
		}
		rollup.sample.TtftHistogram = string(histogram)
		samples = append(samples, rollup.sample)
	}
	if err := model.CreatePerfUserSamples(samples); err != nil {
		restoreUserMetricRollups(rollups)
		common.SysError("failed to flush user performance samples: " + err.Error())
		return err
	}
	return nil
}

func cleanupExpiredUserMetricSamples() {
	cutoff := time.Now().Add(-time.Duration(perf_metrics_setting.UserAnomalyRetentionHours) * time.Hour).Unix()
	if err := model.DeletePerfUserSamplesBefore(cutoff); err != nil {
		common.SysError("failed to cleanup expired user performance samples: " + err.Error())
	}
}

func roundedPercent(value float64) float64 {
	return math.Round(value*100) / 100
}

func buildUserAnomalyItem(aggregate model.PerfUserSampleAggregate, groupAvgTtftMs float64, minRequests int, errorRateThreshold float64) (UserAnomalyItem, bool) {
	errorRate := 0.0
	if aggregate.RequestCount > 0 {
		errorRate = float64(aggregate.ErrorCount) / float64(aggregate.RequestCount) * 100
	}
	aboveAveragePercentage := 0.0
	avgTtftMs := 0.0
	if aggregate.TtftCount > 0 {
		aboveAveragePercentage = float64(aggregate.AboveGroupAvgCount) / float64(aggregate.TtftCount) * 100
		avgTtftMs = float64(aggregate.TtftSumMs) / float64(aggregate.TtftCount)
	}

	ttftAnomaly := aggregate.TtftCount >= int64(minRequests) &&
		groupAvgTtftMs > 0 &&
		aboveAveragePercentage >= perf_metrics_setting.UserTtftAboveAverageRatio
	errorAnomaly := aggregate.RequestCount >= int64(minRequests) && errorRate > errorRateThreshold
	if !ttftAnomaly && !errorAnomaly {
		return UserAnomalyItem{}, false
	}

	return UserAnomalyItem{
		UserID:                  aggregate.UserId,
		Username:                aggregate.Username,
		Group:                   aggregate.Group,
		RequestCount:            aggregate.RequestCount,
		ErrorCount:              aggregate.ErrorCount,
		ErrorRate:               roundedPercent(errorRate),
		TtftCount:               aggregate.TtftCount,
		AvgTtftMs:               math.Round(avgTtftMs),
		GroupAvgTtftMs:          math.Round(groupAvgTtftMs),
		AboveGroupAvgCount:      aggregate.AboveGroupAvgCount,
		AboveGroupAvgPercentage: roundedPercent(aboveAveragePercentage),
		TtftAnomaly:             ttftAnomaly,
		ErrorAnomaly:            errorAnomaly,
		IP:                      aggregate.Ip,
		AccessURL:               aggregate.AccessUrl,
		LastSeenAt:              aggregate.LastSeenAt,
	}, true
}

type userMetricAggregateState struct {
	aggregate model.PerfUserSampleAggregate
	histogram map[int64]int64
}

func aggregateUserMetricRows(rows []model.PerfUserSample) (map[string]model.PerfUserGroupTtftSummary, map[userMetricIdentityKey]*userMetricAggregateState, error) {
	groupSummaries := make(map[string]model.PerfUserGroupTtftSummary)
	userAggregates := make(map[userMetricIdentityKey]*userMetricAggregateState)
	for _, row := range rows {
		if row.RequestCount <= 0 {
			continue
		}
		key := userMetricIdentityKey{userID: row.UserId, group: row.Group}
		state := userAggregates[key]
		if state == nil {
			state = &userMetricAggregateState{
				aggregate: model.PerfUserSampleAggregate{
					UserId:     row.UserId,
					Username:   row.Username,
					Group:      row.Group,
					Ip:         row.Ip,
					AccessUrl:  row.AccessUrl,
					LastSeenAt: row.LastSeenAt,
				},
				histogram: make(map[int64]int64),
			}
			userAggregates[key] = state
		}
		state.aggregate.RequestCount += row.RequestCount
		state.aggregate.ErrorCount += row.ErrorCount
		state.aggregate.TtftCount += row.TtftCount
		state.aggregate.TtftSumMs += row.TtftSumMs
		if row.LastSeenAt >= state.aggregate.LastSeenAt {
			state.aggregate.LastSeenAt = row.LastSeenAt
			if row.Username != "" {
				state.aggregate.Username = row.Username
			}
		}
		if state.aggregate.Ip == "" {
			state.aggregate.Ip = row.Ip
		}
		if state.aggregate.AccessUrl == "" {
			state.aggregate.AccessUrl = row.AccessUrl
		}
		if row.TtftHistogram != "" {
			var histogram map[int64]int64
			if err := common.Unmarshal([]byte(row.TtftHistogram), &histogram); err != nil {
				return nil, nil, fmt.Errorf("decode user performance histogram: %w", err)
			}
			for bucket, count := range histogram {
				state.histogram[bucket] += count
			}
		}
		summary := groupSummaries[row.Group]
		summary.Group = row.Group
		summary.TtftCount += row.TtftCount
		summary.TtftSumMs += row.TtftSumMs
		groupSummaries[row.Group] = summary
	}
	return groupSummaries, userAggregates, nil
}

func histogramCountAboveAverage(histogram map[int64]int64, averageMs float64, overAveragePercent float64) int64 {
	thresholdMs := averageMs * (1 + overAveragePercent/100)
	var count int64
	for bucket, bucketCount := range histogram {
		bucketMidpoint := float64(bucket*userMetricTtftBucketMs) + float64(userMetricTtftBucketMs)/2
		if bucketMidpoint >= thresholdMs {
			count += bucketCount
		}
	}
	return count
}

func normalizeUserAnomalyPage(page int, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultUserAnomalyPageSize
	}
	if pageSize > maxUserAnomalyPageSize {
		pageSize = maxUserAnomalyPageSize
	}
	return page, pageSize
}

func QueryUserAnomalies(username string, page int, pageSize int) (UserAnomalyResult, error) {
	page, pageSize = normalizeUserAnomalyPage(page, pageSize)
	groups := perf_metrics_setting.GetUserAnomalyMonitoredGroups()
	minRequests := perf_metrics_setting.GetUserAnomalyMinRequests()
	errorRateThreshold := perf_metrics_setting.GetUserErrorRateThreshold()
	ttftOverAveragePercent := perf_metrics_setting.GetUserTtftOverAveragePercent()
	result := UserAnomalyResult{
		WindowSeconds:             int64(perf_metrics_setting.UserAnomalyRetentionHours * 3600),
		MinRequests:               minRequests,
		ErrorRateThreshold:        errorRateThreshold,
		TtftAboveAverageThreshold: perf_metrics_setting.UserTtftAboveAverageRatio,
		TtftOverAveragePercent:    ttftOverAveragePercent,
		MonitoredGroups:           groups,
		Page:                      page,
		PageSize:                  pageSize,
		Items:                     []UserAnomalyItem{},
	}
	if len(groups) == 0 {
		return result, nil
	}
	if err := flushUserMetricSamples(); err != nil {
		return result, err
	}
	cleanupExpiredUserMetricSamples()

	startTs := time.Now().Add(-time.Duration(perf_metrics_setting.UserAnomalyRetentionHours) * time.Hour).Unix()
	rows, err := model.ListPerfUserSamples(startTs, groups)
	if err != nil {
		return result, err
	}
	groupSummaries, aggregateStates, err := aggregateUserMetricRows(rows)
	if err != nil {
		return result, err
	}

	allItems := make([]UserAnomalyItem, 0)
	username = strings.TrimSpace(username)
	for key, state := range aggregateStates {
		summary := groupSummaries[key.group]
		groupAverage := 0.0
		if summary.TtftCount > 0 {
			groupAverage = float64(summary.TtftSumMs) / float64(summary.TtftCount)
		}
		state.aggregate.AboveGroupAvgCount = histogramCountAboveAverage(state.histogram, groupAverage, ttftOverAveragePercent)
		if username != "" && state.aggregate.Username != username {
			continue
		}
		item, ok := buildUserAnomalyItem(state.aggregate, groupAverage, minRequests, errorRateThreshold)
		if !ok {
			continue
		}
		allItems = append(allItems, item)
	}

	sort.Slice(allItems, func(i, j int) bool {
		left := allItems[i]
		right := allItems[j]
		if left.ErrorAnomaly != right.ErrorAnomaly {
			return left.ErrorAnomaly
		}
		if left.ErrorRate != right.ErrorRate {
			return left.ErrorRate > right.ErrorRate
		}
		if left.AboveGroupAvgPercentage != right.AboveGroupAvgPercentage {
			return left.AboveGroupAvgPercentage > right.AboveGroupAvgPercentage
		}
		return left.LastSeenAt > right.LastSeenAt
	})

	result.Total = len(allItems)
	if result.Total == 0 {
		return result, nil
	}
	totalPages := (result.Total + pageSize - 1) / pageSize
	if page > totalPages {
		page = totalPages
		result.Page = page
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > result.Total {
		end = result.Total
	}
	result.Items = allItems[start:end]

	// Resolve current names and email addresses only for the visible page.
	// IP and access URL remain the stored observation snapshot and require no
	// per-user lookup on dashboard refresh.
	pageUserIds := make([]int, 0, len(result.Items))
	seenPageUserIds := make(map[int]struct{}, len(result.Items))
	for _, item := range result.Items {
		if _, exists := seenPageUserIds[item.UserID]; exists {
			continue
		}
		seenPageUserIds[item.UserID] = struct{}{}
		pageUserIds = append(pageUserIds, item.UserID)
	}
	contacts, err := model.GetPerfUserContacts(pageUserIds)
	if err != nil {
		return result, err
	}
	contactsById := make(map[int]model.PerfUserContact, len(contacts))
	for _, contact := range contacts {
		contactsById[contact.Id] = contact
	}
	for index := range result.Items {
		if contact, exists := contactsById[result.Items[index].UserID]; exists {
			if contact.Username != "" {
				result.Items[index].Username = contact.Username
			}
			result.Items[index].Email = contact.Email
		}
	}
	return result, nil
}
