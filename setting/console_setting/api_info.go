package console_setting

import (
	"fmt"
	"net/url"
	"strings"
)

type APIInfoDetails struct {
	URL         string
	Route       string
	Description string
}

func ListAPIInfoDetails() []APIInfoDetails {
	items := GetApiInfo()
	details := make([]APIInfoDetails, 0, len(items))
	for _, item := range items {
		configuredURL, _ := item["url"].(string)
		configuredURL = strings.TrimSpace(configuredURL)
		if configuredURL == "" {
			continue
		}
		route, _ := item["route"].(string)
		description, _ := item["description"].(string)
		details = append(details, APIInfoDetails{
			URL:         configuredURL,
			Route:       strings.TrimSpace(route),
			Description: strings.TrimSpace(description),
		})
	}
	return details
}

// FormatAPIInfoSummary returns every configured client-facing API address.
// It is shared by automatic lock responses and manually prepared alerts so
// users receive the same route guidance in both places.
func FormatAPIInfoSummary() string {
	items := ListAPIInfoDetails()
	if len(items) == 0 {
		return "未配置"
	}
	blocks := make([]string, 0, len(items))
	for _, item := range items {
		description := item.Description
		if description == "" {
			description = "未配置"
		}
		blocks = append(blocks, fmt.Sprintf("URL：%s\n说明信息：%s", item.URL, description))
	}
	return strings.Join(blocks, "\n\n")
}

func FindAPIInfo(rawURL string) (APIInfoDetails, bool) {
	target := normalizeAPIInfoURL(rawURL)
	if target == "" {
		return APIInfoDetails{}, false
	}
	for _, item := range ListAPIInfoDetails() {
		if normalizeAPIInfoURL(item.URL) != target {
			continue
		}
		return item, true
	}
	return APIInfoDetails{}, false
}

func normalizeAPIInfoURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path
}
