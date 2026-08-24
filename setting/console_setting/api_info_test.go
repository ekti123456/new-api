package console_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindAPIInfoMatchesOriginWithoutTrailingSlash(t *testing.T) {
	original := consoleSetting
	t.Cleanup(func() { consoleSetting = original })
	consoleSetting.ApiInfo = `[{"url":"https://Chat.Example.com/","route":"primary","description":"Recommended route","color":"blue"}]`

	details, ok := FindAPIInfo("https://chat.example.com")

	require.True(t, ok)
	assert.Equal(t, "https://Chat.Example.com/", details.URL)
	assert.Equal(t, "primary", details.Route)
	assert.Equal(t, "Recommended route", details.Description)
}

func TestFindAPIInfoDoesNotMatchDifferentPath(t *testing.T) {
	original := consoleSetting
	t.Cleanup(func() { consoleSetting = original })
	consoleSetting.ApiInfo = `[{"url":"https://chat.example.com/proxy","route":"proxy","description":"Proxy route","color":"blue"}]`

	_, ok := FindAPIInfo("https://chat.example.com")

	assert.False(t, ok)
}

func TestFormatAPIInfoSummaryIncludesEveryConfiguredURL(t *testing.T) {
	original := consoleSetting
	t.Cleanup(func() { consoleSetting = original })
	consoleSetting.ApiInfo = `[
		{"url":"https://chat.example.com","route":"direct","description":"Direct route","color":"blue"},
		{"url":"https://cf-chat.example.com/","route":"cdn","description":"CDN route","color":"green"}
	]`

	assert.Equal(t,
		"URL：https://chat.example.com\n说明信息：Direct route\n\nURL：https://cf-chat.example.com/\n说明信息：CDN route",
		FormatAPIInfoSummary(),
	)
}

func TestFormatAPIInfoSummaryHandlesEmptyConfiguration(t *testing.T) {
	original := consoleSetting
	t.Cleanup(func() { consoleSetting = original })
	consoleSetting.ApiInfo = ""

	assert.Equal(t, "未配置", FormatAPIInfoSummary())
}
