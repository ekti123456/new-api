package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupLogIPLocationRejectsInvalidIPWithoutNetwork(t *testing.T) {
	_, err := lookupLogIPLocation(context.Background(), nil, "http://unused", "not-an-ip", "zh")
	require.Error(t, err)
}

func TestLookupLogIPLocationHandlesPrivateIPWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()

	result, err := lookupLogIPLocation(context.Background(), server.Client(), server.URL, "10.42.0.1", "zh")
	require.NoError(t, err)
	assert.True(t, result.Local)
	assert.Equal(t, "10.42.0.1", result.IP)
	assert.Equal(t, "内网或本地地址", result.Location)
	assert.Zero(t, calls.Load())
}

func TestLookupLogIPLocationReturnsProviderAddressOnDemand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.RawQuery, "lang=zh-CN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","country":"中国","regionName":"广东","city":"广州","isp":"Example ISP"}`))
	}))
	defer server.Close()

	result, err := lookupLogIPLocation(context.Background(), server.Client(), server.URL, "8.8.8.8", "zh-CN")
	require.NoError(t, err)
	assert.Equal(t, "8.8.8.8", result.IP)
	assert.Equal(t, "中国 · 广东 · 广州", result.Location)
	assert.Equal(t, "Example ISP", result.ISP)
	assert.False(t, result.Local)
}

func TestRequestIPVisibilityFollowsGlobalAdministratorSetting(t *testing.T) {
	previousEnabled := common.RequestIPLogEnabled
	t.Cleanup(func() { common.RequestIPLogEnabled = previousEnabled })

	logs := []*model.Log{{Ip: "203.0.113.9"}}
	common.RequestIPLogEnabled = false
	applyRequestIPVisibility(logs)
	assert.Empty(t, logs[0].Ip)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/log/ip-location?ip=8.8.8.8", nil)
	GetLogIPLocation(context)
	assert.Contains(t, recorder.Body.String(), "请求 IP 日志功能未启用")
}
