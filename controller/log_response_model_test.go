package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestUpstreamResponseModelVisibilityHidesExistingValuesWhenDisabled(t *testing.T) {
	previous := common.UpstreamResponseModelLogEnabled.Load()
	t.Cleanup(func() { common.UpstreamResponseModelLogEnabled.Store(previous) })
	for _, enabled := range []bool{false, true} {
		common.UpstreamResponseModelLogEnabled.Store(enabled)
		logs := []*model.Log{{ModelName: "public-model", Other: `{"upstream_response_model":"gpt-5.6-luna","upstream_response_model_conflict":true,"upstream_model_name":"mapped-model","large_integer":9007199254740993,"cache_tokens":8}`}}
		applyLogFieldVisibility(logs)
		assert.Equal(t, enabled, gjson.Get(logs[0].Other, "upstream_response_model").Exists())
		assert.Equal(t, enabled, gjson.Get(logs[0].Other, "upstream_response_model_conflict").Exists())
		assert.Equal(t, "mapped-model", gjson.Get(logs[0].Other, "upstream_model_name").String())
		assert.Equal(t, "9007199254740993", gjson.Get(logs[0].Other, "large_integer").Raw)
		assert.Equal(t, "public-model", logs[0].ModelName)
	}
}
