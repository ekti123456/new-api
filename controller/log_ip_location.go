package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const (
	logIPLocationBaseURL = "http://ip-api.com/json"
	logIPLocationMaxBody = 64 << 10
)

var logIPLocationHTTPClient = &http.Client{Timeout: 6 * time.Second}

type logIPLocationResult struct {
	IP       string `json:"ip"`
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	City     string `json:"city,omitempty"`
	ISP      string `json:"isp,omitempty"`
	Location string `json:"location"`
	Local    bool   `json:"local,omitempty"`
}

type logIPLocationProviderResponse struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
	ISP        string `json:"isp"`
}

func GetLogIPLocation(c *gin.Context) {
	if !common.RequestIPLogEnabled {
		common.ApiErrorMsg(c, "请求 IP 日志功能未启用")
		return
	}
	result, err := lookupLogIPLocation(c.Request.Context(), logIPLocationHTTPClient, logIPLocationBaseURL, c.Query("ip"), c.Query("lang"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}

func lookupLogIPLocation(ctx context.Context, client *http.Client, baseURL, rawIP, lang string) (logIPLocationResult, error) {
	ip := net.ParseIP(strings.TrimSpace(rawIP))
	if ip == nil {
		return logIPLocationResult{}, errors.New("日志中没有可查询的有效 IP 地址")
	}
	normalizedIP := ip.String()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return logIPLocationResult{IP: normalizedIP, Location: "内网或本地地址", Local: true}, nil
	}
	if client == nil {
		client = logIPLocationHTTPClient
	}
	providerLang := "en"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
		providerLang = "zh-CN"
	}
	endpoint := fmt.Sprintf(
		"%s/%s?lang=%s&fields=status,message,country,regionName,city,isp",
		strings.TrimRight(baseURL, "/"), url.PathEscape(normalizedIP), url.QueryEscape(providerLang),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return logIPLocationResult{}, errors.New("创建 IP 归属地查询失败")
	}
	resp, err := client.Do(req)
	if err != nil {
		return logIPLocationResult{}, errors.New("IP 归属地服务暂时不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return logIPLocationResult{}, fmt.Errorf("IP 归属地服务返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, logIPLocationMaxBody+1))
	if err != nil || len(body) > logIPLocationMaxBody {
		return logIPLocationResult{}, errors.New("读取 IP 归属地结果失败")
	}
	var provider logIPLocationProviderResponse
	if err := common.Unmarshal(body, &provider); err != nil {
		return logIPLocationResult{}, errors.New("IP 归属地服务返回了无效结果")
	}
	if provider.Status != "success" {
		message := strings.TrimSpace(provider.Message)
		if message == "" {
			message = "查询失败"
		}
		return logIPLocationResult{}, fmt.Errorf("IP 归属地查询失败：%s", message)
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{provider.Country, provider.RegionName, provider.City} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	location := strings.Join(parts, " · ")
	if location == "" {
		location = "未知地区"
	}
	return logIPLocationResult{
		IP: normalizedIP, Country: strings.TrimSpace(provider.Country), Region: strings.TrimSpace(provider.RegionName),
		City: strings.TrimSpace(provider.City), ISP: strings.TrimSpace(provider.ISP), Location: location,
	}, nil
}
