package controller

import (
	"html"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const (
	userAlertChannelInApp    = "in_app"
	userAlertChannelEmail    = "email"
	userAlertMaxTitleRunes   = 200
	userAlertMaxContentRunes = 20000
)

type sendUserAlertRequest struct {
	UserId  int    `json:"user_id"`
	Channel string `json:"channel"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

func normalizeUserAlertText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func normalizeUserAlertTitle(value string) string {
	value = normalizeUserAlertText(value)
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, value)
}

func GetPersonalNotifications(c *gin.Context) {
	items, unreadCount, err := model.ListUserNotifications(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":        items,
			"unread_count": unreadCount,
		},
	})
}

func MarkPersonalNotificationsRead(c *gin.Context) {
	if err := model.MarkUserNotificationsRead(c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func SendUserPerformanceAlert(c *gin.Context) {
	var req sendUserAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request")
		return
	}
	req.Channel = strings.TrimSpace(req.Channel)
	req.Title = normalizeUserAlertTitle(req.Title)
	req.Content = normalizeUserAlertText(req.Content)
	if req.UserId <= 0 {
		common.ApiErrorMsg(c, "invalid user id")
		return
	}
	if req.Channel != userAlertChannelInApp && req.Channel != userAlertChannelEmail {
		common.ApiErrorMsg(c, "invalid delivery channel")
		return
	}
	if req.Title == "" || utf8.RuneCountInString(req.Title) > userAlertMaxTitleRunes {
		common.ApiErrorMsg(c, "title must contain between 1 and 200 characters")
		return
	}
	if req.Content == "" || utf8.RuneCountInString(req.Content) > userAlertMaxContentRunes {
		common.ApiErrorMsg(c, "content must contain between 1 and 20000 characters")
		return
	}

	user, err := model.GetUserById(req.UserId, false)
	if err != nil {
		common.ApiErrorMsg(c, "target user does not exist")
		return
	}

	switch req.Channel {
	case userAlertChannelInApp:
		err = model.CreateUserNotification(&model.UserNotification{
			UserId:    user.Id,
			Title:     req.Title,
			Content:   req.Content,
			CreatedBy: c.GetInt("id"),
		})
	case userAlertChannelEmail:
		emailAddress := strings.TrimSpace(user.Email)
		if emailAddress == "" {
			common.ApiErrorMsg(c, "target user has no email address")
			return
		}
		emailContent := "<div style=\"font-family:Arial,sans-serif;line-height:1.7;white-space:normal\">" +
			strings.ReplaceAll(html.EscapeString(req.Content), "\n", "<br>\n") + "</div>"
		err = common.SendEmail(req.Title, emailAddress, emailContent)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.SysLog("root user sent a " + req.Channel + " performance alert to user " + user.Username)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}
