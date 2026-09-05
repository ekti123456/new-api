package model

import "time"

const perfMetricErrorRetention = 48 * time.Hour

// PerfMetricError stores one final failed relay request for the administrator
// performance dashboard. It is deliberately separate from the user-facing log
// table because error logs can be disabled while performance metrics remain
// enabled.
type PerfMetricError struct {
	Id                int64  `json:"id" gorm:"primaryKey"`
	CreatedAt         int64  `json:"created_at" gorm:"index"`
	UserId            int    `json:"user_id" gorm:"index"`
	Username          string `json:"username" gorm:"size:128;index"`
	ModelName         string `json:"model_name" gorm:"size:128;index"`
	Group             string `json:"group" gorm:"column:group;size:64;index"`
	ChannelId         int    `json:"channel_id" gorm:"index"`
	ChannelName       string `json:"channel_name,omitempty" gorm:"size:128"`
	TokenId           int    `json:"token_id,omitempty"`
	RequestId         string `json:"request_id,omitempty" gorm:"size:128;index"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"size:128;index"`
	ErrorType         string `json:"error_type" gorm:"size:128;index"`
	ErrorCode         string `json:"error_code" gorm:"size:128;index"`
	StatusCode        int    `json:"status_code" gorm:"index"`
	ErrorReason       string `json:"error_reason" gorm:"type:text"`
	RequestPath       string `json:"request_path,omitempty" gorm:"size:256"`
	UserAgent         string `json:"user_agent,omitempty" gorm:"size:512"`
}

func (PerfMetricError) TableName() string {
	return "perf_metric_errors"
}

type PerfMetricErrorQuery struct {
	ModelName      string
	Group          string
	Username       string
	ErrorType      string
	ErrorCode      string
	UserID         int
	StatusCode     int
	StartTimestamp int64
	EndTimestamp   int64
	StartIndex     int
	PageSize       int
}

type PerfMetricErrorPage struct {
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
	Total    int64             `json:"total"`
	Items    []PerfMetricError `json:"items"`
}

func ListPerfMetricErrors(query PerfMetricErrorQuery) (PerfMetricErrorPage, error) {
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	startIndex := query.StartIndex
	if startIndex < 0 {
		startIndex = 0
	}

	tx := DB.Model(&PerfMetricError{}).
		Where("created_at >= ?", time.Now().Add(-perfMetricErrorRetention).Unix())
	if query.ModelName != "" {
		tx = tx.Where("model_name = ?", query.ModelName)
	}
	if query.Group != "" {
		tx = tx.Where(commonGroupCol+" = ?", query.Group)
	}
	if query.Username != "" {
		tx = tx.Where("username = ?", query.Username)
	}
	if query.ErrorType != "" {
		tx = tx.Where("error_type = ?", query.ErrorType)
	}
	if query.ErrorCode != "" {
		tx = tx.Where("error_code = ?", query.ErrorCode)
	}
	if query.UserID > 0 {
		tx = tx.Where("user_id = ?", query.UserID)
	}
	if query.StatusCode > 0 {
		tx = tx.Where("status_code = ?", query.StatusCode)
	}
	if query.StartTimestamp > 0 {
		tx = tx.Where("created_at >= ?", query.StartTimestamp)
	}
	if query.EndTimestamp > 0 {
		tx = tx.Where("created_at <= ?", query.EndTimestamp)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return PerfMetricErrorPage{}, err
	}
	items := make([]PerfMetricError, 0, pageSize)
	if err := tx.Order("created_at desc, id desc").Limit(pageSize).Offset(startIndex).Find(&items).Error; err != nil {
		return PerfMetricErrorPage{}, err
	}

	page := 1
	if pageSize > 0 {
		page = startIndex/pageSize + 1
	}
	return PerfMetricErrorPage{Page: page, PageSize: pageSize, Total: total, Items: items}, nil
}

func CreatePerfMetricError(item *PerfMetricError) error {
	if item == nil || item.ModelName == "" {
		return nil
	}
	if item.CreatedAt <= 0 {
		item.CreatedAt = time.Now().Unix()
	}
	return DB.Create(item).Error
}

func DeleteExpiredPerfMetricErrors(now time.Time) error {
	cutoffTs := now.Add(-perfMetricErrorRetention).Unix()
	if cutoffTs <= 0 {
		return nil
	}
	for {
		var expiredIDs []int64
		if err := DB.Model(&PerfMetricError{}).
			Where("created_at < ?", cutoffTs).
			Order("created_at ASC, id ASC").Limit(500).
			Pluck("id", &expiredIDs).Error; err != nil {
			return err
		}
		if len(expiredIDs) == 0 {
			return nil
		}
		if err := DB.Where("id IN ? AND created_at < ?", expiredIDs, cutoffTs).
			Delete(&PerfMetricError{}).Error; err != nil {
			return err
		}
	}
}
