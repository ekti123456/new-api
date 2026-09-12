package model

import (
	"crypto/sha256"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const perfMetricErrorGroupColumns = "COALESCE(user_id, 0), COALESCE(status_code, 0), COALESCE(error_type, ''), COALESCE(error_code, ''), CASE WHEN COALESCE(error_code, '') = '' THEN COALESCE(error_reason, '') ELSE '' END"

func perfMetricErrorGroupQuery(query *gorm.DB, reference PerfMetricError) *gorm.DB {
	query = query.Where("COALESCE(user_id, 0) = ? AND COALESCE(status_code, 0) = ? AND COALESCE(error_type, '') = ? AND COALESCE(error_code, '') = ?",
		reference.UserId, reference.StatusCode, reference.ErrorType, reference.ErrorCode)
	if reference.ErrorCode == "" {
		query = query.Where("COALESCE(error_reason, '') = ?", reference.ErrorReason)
	}
	return query
}

func listPerfMetricErrorGroups(query *gorm.DB, page PerfMetricErrorPage, startIndex int) (PerfMetricErrorPage, error) {
	grouped := query.Select("MAX(id) AS id, COUNT(*) AS occurrence_count, MIN(created_at) AS first_seen, MAX(created_at) AS last_seen").Group(perfMetricErrorGroupColumns)
	var totals struct {
		Total            int64
		TotalOccurrences int64
	}
	if err := DB.Table("(?) AS error_groups", grouped).
		Select("COUNT(*) AS total, COALESCE(SUM(occurrence_count), 0) AS total_occurrences").Scan(&totals).Error; err != nil {
		return page, err
	}
	page.Total = totals.Total
	page.TotalOccurrences = totals.TotalOccurrences
	var groups []struct {
		Id              int64
		OccurrenceCount int64
		FirstSeen       int64
		LastSeen        int64
	}
	if err := grouped.Order("last_seen DESC, id DESC").Limit(page.PageSize).Offset(startIndex).Scan(&groups).Error; err != nil {
		return page, err
	}
	if len(groups) == 0 {
		return page, nil
	}
	ids := make([]int64, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.Id)
	}
	var references []PerfMetricError
	if err := query.Where("id IN ?", ids).Find(&references).Error; err != nil {
		return page, err
	}
	referencesByID := make(map[int64]PerfMetricError, len(references))
	for _, reference := range references {
		referencesByID[reference.Id] = reference
	}
	for _, group := range groups {
		reference, found := referencesByID[group.Id]
		if !found {
			continue
		}
		latest := reference
		if reference.CreatedAt != group.LastSeen {
			latest = PerfMetricError{}
			if err := perfMetricErrorGroupQuery(query, reference).Where("id <= ?", reference.Id).
				Order("created_at DESC, id DESC").First(&latest).Error; err != nil {
				return page, err
			}
		}
		reasonKey := ""
		if reference.ErrorCode == "" {
			reasonKey = reference.ErrorReason
		}
		key, err := common.Marshal([]any{reference.UserId, reference.StatusCode, reference.ErrorType, reference.ErrorCode, reasonKey})
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, PerfMetricErrorItem{
			PerfMetricError: latest, GroupKey: fmt.Sprintf("%x", sha256.Sum256(key)), ErrorGroupID: reference.Id,
			OccurrenceCount: group.OccurrenceCount, FirstSeen: group.FirstSeen, LastSeen: group.LastSeen,
		})
	}
	return page, nil
}
