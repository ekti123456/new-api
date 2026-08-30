package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetReferralSummary(c *gin.Context) {
	summary, err := model.GetReferralSummary(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func GetReferralCommissions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter := model.ReferralCommissionFilter{
		Keyword:       strings.TrimSpace(c.Query("keyword")),
		PaymentMethod: strings.TrimSpace(c.Query("payment_method")),
	}
	if rawStartTime := c.Query("start_time"); rawStartTime != "" {
		startTime, err := strconv.ParseInt(rawStartTime, 10, 64)
		if err != nil || startTime <= 0 {
			common.ApiErrorMsg(c, "invalid start_time")
			return
		}
		filter.StartTime = startTime
	}
	if rawEndTime := c.Query("end_time"); rawEndTime != "" {
		endTime, err := strconv.ParseInt(rawEndTime, 10, 64)
		if err != nil || endTime <= 0 {
			common.ApiErrorMsg(c, "invalid end_time")
			return
		}
		filter.EndTime = endTime
	}
	if filter.StartTime > 0 && filter.EndTime > 0 && filter.StartTime > filter.EndTime {
		common.ApiErrorMsg(c, "invalid time range")
		return
	}

	commissions, total, err := model.GetReferralCommissions(c.GetInt("id"), pageInfo, filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(commissions)
	common.ApiSuccess(c, pageInfo)
}

func GetReferralMembers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter := model.ReferralMemberFilter{
		Keyword: strings.TrimSpace(c.Query("keyword")),
		Status:  strings.ToLower(strings.TrimSpace(c.Query("status"))),
	}
	if filter.Status != "" && filter.Status != model.ReferralMemberStatusQualified && filter.Status != model.ReferralMemberStatusPending {
		common.ApiErrorMsg(c, "invalid status")
		return
	}
	members, total, err := model.GetReferralMembers(c.GetInt("id"), pageInfo, filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(members)
	common.ApiSuccess(c, pageInfo)
}
