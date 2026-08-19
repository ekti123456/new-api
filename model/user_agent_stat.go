package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	unknownUserAgentFamily  = "Unknown"
	otherUserAgentFamily    = "Other"
	maxUserAgentFamilyBytes = 64
	maxUserAgentFamilies    = 9
)

type UserAgentStat struct {
	Id           int    `json:"id"`
	UserID       int    `json:"user_id" gorm:"index:idx_uas_user_hour_family,priority:1"`
	Username     string `json:"username" gorm:"index;index:idx_uas_user_hour_family,priority:2;size:64;default:''"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;index;index:idx_uas_user_hour_family,priority:3"`
	ClientFamily string `json:"client_family" gorm:"size:64;index:idx_uas_user_hour_family,priority:4;default:''"`
	Count        int64  `json:"count" gorm:"bigint;default:0"`
}

type UserAgentStatItem struct {
	ClientFamily string  `json:"client_family"`
	Count        int64   `json:"count"`
	Percentage   float64 `json:"percentage"`
	IsOther      bool    `json:"is_other,omitempty"`
}

type UserAgentStatsResult struct {
	Total int64               `json:"total"`
	Items []UserAgentStatItem `json:"items"`
}

var userAgentStatCache = make(map[string]*UserAgentStat)
var userAgentStatCacheLock sync.Mutex

func normalizeUserAgentFamily(userAgent string) string {
	userAgent = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, userAgent)
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return unknownUserAgentFamily
	}

	lowerUserAgent := strings.ToLower(userAgent)
	if strings.HasPrefix(lowerUserAgent, "codex desktop/") || strings.Contains(lowerUserAgent, "(codex desktop;") {
		return "Codex Desktop"
	}
	if strings.HasPrefix(lowerUserAgent, "codex-tui/") || strings.Contains(lowerUserAgent, "(codex-tui;") {
		return "codex-tui"
	}

	family := userAgent
	if slashIndex := strings.IndexByte(userAgent, '/'); slashIndex > 0 {
		family = userAgent[:slashIndex]
	}
	family = strings.TrimSpace(family)
	if family == "" {
		return unknownUserAgentFamily
	}

	switch strings.ToLower(family) {
	case "mozilla":
		family = "Mozilla"
	case "postmanruntime":
		family = "PostmanRuntime"
	case "curl":
		family = "curl"
	case "python-requests":
		family = "python-requests"
	}
	if len(family) > maxUserAgentFamilyBytes {
		family = family[:maxUserAgentFamilyBytes]
		for !utf8.ValidString(family) {
			family = family[:len(family)-1]
		}
	}
	return family
}

func LogUserAgentStat(userID int, username string, createdAt int64, userAgent string) {
	stat := &UserAgentStat{
		UserID:       userID,
		Username:     username,
		CreatedAt:    createdAt - (createdAt % 3600),
		ClientFamily: normalizeUserAgentFamily(userAgent),
		Count:        1,
	}
	key := fmt.Sprintf("%d\x00%s\x00%d\x00%s", stat.UserID, stat.Username, stat.CreatedAt, stat.ClientFamily)

	userAgentStatCacheLock.Lock()
	defer userAgentStatCacheLock.Unlock()
	if cached, ok := userAgentStatCache[key]; ok {
		cached.Count++
		return
	}
	userAgentStatCache[key] = stat
}

func SaveUserAgentStatCache() {
	userAgentStatCacheLock.Lock()
	defer userAgentStatCacheLock.Unlock()
	if len(userAgentStatCache) == 0 {
		return
	}

	for _, stat := range userAgentStatCache {
		stored := &UserAgentStat{}
		DB.Where("user_id = ? AND username = ? AND created_at = ? AND client_family = ?", stat.UserID, stat.Username, stat.CreatedAt, stat.ClientFamily).
			First(stored)
		if stored.Id > 0 {
			if err := DB.Model(&UserAgentStat{}).
				Where("id = ?", stored.Id).
				UpdateColumn("count", gorm.Expr("count + ?", stat.Count)).Error; err != nil {
				common.SysLog("increase user agent stat error: " + err.Error())
			}
			continue
		}
		if err := DB.Create(stat).Error; err != nil {
			common.SysLog("create user agent stat error: " + err.Error())
		}
	}
	userAgentStatCache = make(map[string]*UserAgentStat)
}

func GetUserAgentStats(startTime int64, endTime int64, username string) (*UserAgentStatsResult, error) {
	rows := make([]UserAgentStatItem, 0)
	query := DB.Model(&UserAgentStat{}).
		Select("client_family, sum(count) as count").
		Where("created_at >= ? AND created_at <= ?", startTime, endTime)
	if username != "" {
		query = query.Where("username = ?", username)
	}
	if err := query.Group("client_family").Find(&rows).Error; err != nil {
		return nil, err
	}

	var total int64
	for _, row := range rows {
		total += row.Count
	}
	if total == 0 {
		return &UserAgentStatsResult{Items: []UserAgentStatItem{}}, nil
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Count > rows[j].Count
	})
	items := make([]UserAgentStatItem, 0, len(rows))
	var otherCount int64
	for index, row := range rows {
		percentage := float64(row.Count) / float64(total) * 100
		if index >= maxUserAgentFamilies {
			otherCount += row.Count
			continue
		}
		row.Percentage = percentage
		items = append(items, row)
	}
	if otherCount > 0 {
		items = append(items, UserAgentStatItem{
			ClientFamily: otherUserAgentFamily,
			Count:        otherCount,
			Percentage:   float64(otherCount) / float64(total) * 100,
			IsOther:      true,
		})
	}
	return &UserAgentStatsResult{Total: total, Items: items}, nil
}
