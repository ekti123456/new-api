package controller

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"

	"github.com/gin-gonic/gin"
)

func parseFlowQuotaTimeRange(c *gin.Context) (int64, int64, bool) {
	startTimestamp, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid start_timestamp")
		return 0, 0, false
	}
	endTimestamp, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid end_timestamp")
		return 0, 0, false
	}
	if endTimestamp < startTimestamp {
		common.ApiErrorMsg(c, "invalid time range")
		return 0, 0, false
	}
	return startTimestamp, endTimestamp, true
}

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	dates, err := model.GetAllQuotaDates(startTimestamp, endTimestamp, username)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	dates, err := model.GetQuotaDataGroupByUser(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetUserAgentStats(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	stats, err := model.GetUserAgentStats(startTimestamp, endTimestamp, c.Query("username"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}

func GetUserPerformanceAnomalies(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	result, err := perfmetrics.QueryUserAnomalies(c.Query("username"), page, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    result,
	})
}

type fullUserSessionWindowItem struct {
	UserID          int                                   `json:"user_id"`
	Username        string                                `json:"username"`
	DisplayName     string                                `json:"display_name,omitempty"`
	Group           string                                `json:"group,omitempty"`
	FullTargetCount int                                   `json:"full_target_count"`
	UpdatedAt       time.Time                             `json:"updated_at"`
	Targets         []model.UserSessionWindowTargetStatus `json:"targets"`
}

// GetFullUserSessionWindows exposes the latest Codex2API session-capacity
// snapshots to the Root dashboard. Status is grouped by person for display,
// but every target remains separate so one full target cannot be hidden by
// another target that still has spare capacity.
func GetFullUserSessionWindows(c *gin.Context) {
	targets := model.ListUserSessionWindowTargetStatuses(true)
	if len(targets) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    gin.H{"full_user_count": 0, "full_target_count": 0, "items": []fullUserSessionWindowItem{}},
		})
		return
	}

	userIDs := make([]int, 0, len(targets))
	seenUserIDs := make(map[int]struct{}, len(targets))
	for _, target := range targets {
		if _, exists := seenUserIDs[target.UserID]; exists {
			continue
		}
		seenUserIDs[target.UserID] = struct{}{}
		userIDs = append(userIDs, target.UserID)
	}
	users := make([]model.User, 0, len(userIDs))
	if err := model.DB.Model(&model.User{}).
		Select("id", "username", "display_name", "group").
		Where("id IN ?", userIDs).
		Find(&users).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	usersByID := make(map[int]model.User, len(users))
	for _, user := range users {
		usersByID[user.Id] = user
	}

	itemsByUserID := make(map[int]*fullUserSessionWindowItem, len(userIDs))
	for _, target := range targets {
		item := itemsByUserID[target.UserID]
		if item == nil {
			user := usersByID[target.UserID]
			item = &fullUserSessionWindowItem{
				UserID: target.UserID, Username: user.Username, DisplayName: user.DisplayName,
				Group: user.Group, Targets: make([]model.UserSessionWindowTargetStatus, 0, 1),
			}
			itemsByUserID[target.UserID] = item
		}
		item.Targets = append(item.Targets, target)
		item.FullTargetCount++
		if target.UpdatedAt.After(item.UpdatedAt) {
			item.UpdatedAt = target.UpdatedAt
		}
	}
	items := make([]fullUserSessionWindowItem, 0, len(itemsByUserID))
	for _, item := range itemsByUserID {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].FullTargetCount != items[j].FullTargetCount {
			return items[i].FullTargetCount > items[j].FullTargetCount
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].UserID < items[j].UserID
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"full_user_count": len(items), "full_target_count": len(targets), "items": items,
		},
	})
}

func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 判断时间跨度是否超过 1 个月
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := model.GetQuotaDataByUserId(userId, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetAllFlowQuotaDates(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	username := c.Query("username")
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, username, 0, c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetUserFlowQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, "", userId, common.RoleCommonUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}
