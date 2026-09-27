package model

import "context"

// ProjectIntegrationChannels is internal-only: credentials are used solely to
// scope aggregation and must never be serialized into an API response.
func ProjectIntegrationChannels(ctx context.Context) ([]*Channel, error) {
	var rows []*Channel
	err := DB.WithContext(ctx).Select("id", "name", "key", "base_url", "status").Find(&rows).Error
	return rows, err
}

type ProjectIntegrationUsage struct {
	RPM      int64 `json:"rpm"`
	Requests int64 `json:"requests"`
	Quota    int64 `json:"quota"`
}

func GetProjectIntegrationUsage(ctx context.Context, ids []int, start, now int64) (ProjectIntegrationUsage, error) {
	var out ProjectIntegrationUsage
	if len(ids) == 0 {
		return out, nil
	}
	// Both queries deliberately use LOG_DB, including a separately configured log DB.
	err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("channel_id IN ? AND type = ? AND created_at >= ? AND created_at <= ?", ids, LogTypeConsume, start, now).
		Select("COUNT(*) AS requests, COALESCE(SUM(quota), 0) AS quota").Scan(&out).Error
	if err != nil {
		return out, err
	}
	err = LOG_DB.WithContext(ctx).Model(&Log{}).Where("channel_id IN ? AND type = ? AND created_at > ? AND created_at <= ?", ids, LogTypeConsume, now-60, now).Count(&out.RPM).Error
	return out, err
}
