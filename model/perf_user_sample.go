package model

// PerfUserSample stores a short-lived rollup instead of one database row per
// relay. The in-memory collector aggregates requests by user and group, then
// persists counts and a compact TTFT histogram in batches.
type PerfUserSample struct {
	Id            int64  `json:"id" gorm:"primaryKey"`
	UserId        int    `json:"user_id" gorm:"index:idx_perf_user_group_last_seen,priority:1"`
	Username      string `json:"username" gorm:"size:128"`
	Group         string `json:"group" gorm:"column:group;size:64;index:idx_perf_user_group_last_seen,priority:2;index:idx_perf_group_last_seen,priority:1"`
	CreatedAt     int64  `json:"created_at" gorm:"index"`
	LastSeenAt    int64  `json:"last_seen_at" gorm:"index:idx_perf_user_group_last_seen,priority:3;index:idx_perf_group_last_seen,priority:2;index"`
	RequestCount  int64  `json:"request_count" gorm:"default:0"`
	ErrorCount    int64  `json:"error_count" gorm:"default:0"`
	TtftCount     int64  `json:"ttft_count" gorm:"default:0"`
	TtftSumMs     int64  `json:"ttft_sum_ms" gorm:"default:0"`
	TtftHistogram string `json:"ttft_histogram" gorm:"type:text"`
	Ip            string `json:"ip" gorm:"size:64"`
	AccessUrl     string `json:"access_url" gorm:"size:512"`
}

func (PerfUserSample) TableName() string {
	return "perf_user_samples"
}

func CreatePerfUserSamples(samples []PerfUserSample) error {
	if len(samples) == 0 {
		return nil
	}
	return DB.CreateInBatches(samples, 500).Error
}

func ListPerfUserSamples(startTs int64, groups []string) ([]PerfUserSample, error) {
	samples := make([]PerfUserSample, 0)
	if len(groups) == 0 {
		return samples, nil
	}
	err := DB.Where("last_seen_at >= ?", startTs).
		Where(commonGroupCol+" IN ?", groups).
		Order("last_seen_at ASC, id ASC").
		Find(&samples).Error
	return samples, err
}

type PerfUserGroupTtftSummary struct {
	Group     string `json:"group"`
	TtftSumMs int64  `json:"ttft_sum_ms"`
	TtftCount int64  `json:"ttft_count"`
}

type PerfUserSampleAggregate struct {
	UserId             int    `json:"user_id"`
	Username           string `json:"username"`
	Group              string `json:"group"`
	RequestCount       int64  `json:"request_count"`
	ErrorCount         int64  `json:"error_count"`
	TtftCount          int64  `json:"ttft_count"`
	TtftSumMs          int64  `json:"ttft_sum_ms"`
	AboveGroupAvgCount int64  `json:"above_group_avg_count"`
	Ip                 string `json:"ip"`
	AccessUrl          string `json:"access_url"`
	LastSeenAt         int64  `json:"last_seen_at"`
}

func DeletePerfUserSamplesBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	return DB.Where("last_seen_at < ?", cutoffTs).Delete(&PerfUserSample{}).Error
}

type PerfUserContact struct {
	Id       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

func GetPerfUserContacts(userIds []int) ([]PerfUserContact, error) {
	contacts := make([]PerfUserContact, 0, len(userIds))
	if len(userIds) == 0 {
		return contacts, nil
	}
	err := DB.Model(&User{}).
		Select("id", "username", "email").
		Where("id IN ?", userIds).
		Find(&contacts).Error
	return contacts, err
}
