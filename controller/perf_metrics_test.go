package controller

import (
	"testing"

	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivePerfMetricGroupsFollowUserUsableGroups(t *testing.T) {
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
	})

	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组","gpt-pro":"专业分组"}`))

	assert.ElementsMatch(t, []string{"default", "gpt-pro"}, activePerfMetricGroups())

	filtered := filterActiveGroups([]perfmetrics.GroupResult{
		{Group: "default"},
		{Group: "gpt-pro"},
		{Group: "gpt-pro-unlimited"},
		{Group: "historical"},
		{Group: "auto"},
	})

	assert.Equal(t, []perfmetrics.GroupResult{
		{Group: "default"},
		{Group: "gpt-pro"},
	}, filtered)
}
