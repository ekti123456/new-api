package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListPerfMetricErrorsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&PerfMetricError{}))
	DB.Where("1 = 1").Delete(&PerfMetricError{})
	t.Cleanup(func() { DB.Where("1 = 1").Delete(&PerfMetricError{}) })

	items := []PerfMetricError{
		{CreatedAt: 100, UserId: 1, Username: "alice", ModelName: "gpt-a", Group: "pro", StatusCode: 503, ErrorType: "upstream", ErrorCode: "no_channel", ErrorReason: "first"},
		{CreatedAt: 200, UserId: 2, Username: "bob", ModelName: "gpt-a", Group: "pro", StatusCode: 429, ErrorType: "upstream", ErrorCode: "rate_limited", ErrorReason: "second"},
		{CreatedAt: 300, UserId: 1, Username: "alice", ModelName: "gpt-b", Group: "default", StatusCode: 500, ErrorType: "transport", ErrorCode: "closed", ErrorReason: "third"},
	}
	require.NoError(t, DB.Create(&items).Error)

	page, err := ListPerfMetricErrors(PerfMetricErrorQuery{
		ModelName: "gpt-a", Group: "pro", StartIndex: 0, PageSize: 1,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, 1, page.Page)
	require.Len(t, page.Items, 1)
	require.Equal(t, "second", page.Items[0].ErrorReason)

	filtered, err := ListPerfMetricErrors(PerfMetricErrorQuery{
		Username: "alice", UserID: 1, StatusCode: 500, ErrorType: "transport", ErrorCode: "closed", StartIndex: 0, PageSize: 20,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), filtered.Total)
	require.Len(t, filtered.Items, 1)
	require.Equal(t, "gpt-b", filtered.Items[0].ModelName)
}
