package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var responsesWSTestUserSequence atomic.Int64

func TestResponsesWebSocketEnforcesRPMOnEveryFrame(t *testing.T) {
	var calls atomic.Int32
	fixture := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
			calls.Add(1)
			if ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"rpm-first","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`)) != nil {
				return
			}
		}
	})
	previousEnabled, previousModels := setting.ModelRPMRateLimitEnabled, setting.ModelRPMRateLimitModels2JSONString()
	setting.ModelRPMRateLimitEnabled = true
	require.NoError(t, setting.UpdateModelRPMRateLimitModelsByJSONString(`{"ws-billing":1}`))
	t.Cleanup(func() {
		setting.ModelRPMRateLimitEnabled = previousEnabled
		require.NoError(t, setting.UpdateModelRPMRateLimitModelsByJSONString(previousModels))
	})
	for _, expected := range []string{"response.completed", "error"} {
		require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"same connection, separate request"}`)))
		event := readResponsesWSTestEvent(t, fixture.client)
		require.Equal(t, expected, event["type"])
		if expected == "error" {
			require.Equal(t, float64(429), event["status"])
		}
	}
	assert.Equal(t, int32(1), calls.Load())
	fixture.closeAndWait(t)
	assertResponsesWSAccounting(t, fixture, []int{10})
}

func TestResponsesWebSocketChannelDisableClosesIdleConnection(t *testing.T) {
	fixture := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		if ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"before-disable","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`)) != nil {
			return
		}
		_, _, _ = ws.ReadMessage()
	})
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"first"}`)))
	require.Equal(t, "response.completed", readResponsesWSTestEvent(t, fixture.client)["type"])
	engine := gin.New()
	engine.PUT("/channel/:id/status", UpdateChannelStatus)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/channel/%d/status", fixture.channel.Id), strings.NewReader(`{"status":2}`)))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, fixture.client.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err := fixture.client.ReadMessage()
	require.True(t, websocket.IsCloseError(err, websocket.ClosePolicyViolation), "expected immediate revocation, got %v", err)
	fixture.closeAndWait(t)
	assertResponsesWSAccounting(t, fixture, []int{10})
}

func TestResponsesWebSocketRevalidatesConnectionAdmission(t *testing.T) {
	for _, scenario := range []string{"token_disabled", "model_revoked", "channel_disabled", "channel_key_changed", "proxy_changed"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			fixture := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
				for {
					_, _, err := ws.ReadMessage()
					if err != nil {
						return
					}
					calls.Add(1)
					if ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"first","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`)) != nil {
						return
					}
				}
			})
			require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"first"}`)))
			require.Equal(t, "response.completed", readResponsesWSTestEvent(t, fixture.client)["type"])
			switch scenario {
			case "token_disabled", "model_revoked":
				require.NoError(t, model.DB.First(fixture.token, fixture.token.Id).Error)
				if scenario == "token_disabled" {
					fixture.token.Status = common.TokenStatusDisabled
				} else {
					fixture.token.ModelLimitsEnabled = true
					fixture.token.ModelLimits = "other-model"
				}
				require.NoError(t, fixture.token.Update())
			case "channel_disabled":
				require.NoError(t, model.DB.Model(fixture.channel).Update("status", common.ChannelStatusManuallyDisabled).Error)
			case "channel_key_changed":
				require.NoError(t, model.DB.Model(fixture.channel).Update("key", "different-key").Error)
			case "proxy_changed":
				settings := fixture.channel.GetSetting()
				settings.Proxy = "http://127.0.0.1:1"
				fixture.channel.SetSetting(settings)
				require.NoError(t, model.DB.Save(fixture.channel).Error)
			}
			require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","previous_response_id":"first","input":"second"}`)))
			event := readResponsesWSTestEvent(t, fixture.client)
			require.Equal(t, "error", event["type"])
			assert.Contains(t, []float64{400, 401, 403, 409, 503}, event["status"], "event=%v", event)
			assert.Equal(t, int32(1), calls.Load(), "revoked connection must not send the second frame")
			fixture.closeAndWait(t)
			assertResponsesWSAccounting(t, fixture, []int{10})
		})
	}
}

func TestResponsesWebSocketKeepsImagesAndToolNamespacesAcrossFrames(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			received := make(chan map[string]any, 2)
			fixture := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
				for index := 1; index <= 2; index++ {
					_, raw, err := ws.ReadMessage()
					if !assert.NoError(t, err) {
						return
					}
					var frame map[string]any
					if !assert.NoError(t, common.Unmarshal(raw, &frame)) {
						return
					}
					received <- frame
					if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"r%d","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`, index)))) {
						return
					}
				}
				_, _, _ = ws.ReadMessage()
			})
			settings := fixture.channel.GetSetting()
			settings.PassThroughBodyEnabled = passthrough
			fixture.channel.SetSetting(settings)
			require.NoError(t, model.DB.Save(fixture.channel).Error)
			frames := []string{
				`{"type":"response.create","model":"ws-billing","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,ORIGINAL","detail":"original"}]},{"type":"function_call","name":"js","namespace":"functions","call_id":"call-1","arguments":"{}"}],"tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"js","parameters":{"type":"object"}}]}]}`,
				`{"type":"response.create","model":"ws-billing","store":false,"previous_response_id":"r1","input":[{"type":"function_call_output","call_id":"call-1","output":"result"},{"role":"user","content":"next"}]}`,
			}
			for index, frame := range frames {
				require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(frame)))
				require.Equal(t, "response.completed", readResponsesWSTestEvent(t, fixture.client)["type"])
				actual := <-received
				assert.NotContains(t, actual, "stream", "HTTP-shaped middleware must not inject stream into WS wire payload")
				assert.Equal(t, false, actual["store"])
				input := actual["input"].([]any)
				if index == 0 {
					image := input[0].(map[string]any)["content"].([]any)[0].(map[string]any)
					assert.Equal(t, "data:image/png;base64,ORIGINAL", image["image_url"])
					assert.Equal(t, "original", image["detail"])
					assert.Equal(t, "functions", input[1].(map[string]any)["namespace"])
				} else {
					assert.Equal(t, "r1", actual["previous_response_id"])
					assert.Len(t, input, 2)
					assert.Equal(t, "call-1", input[0].(map[string]any)["call_id"])
					raw, err := common.Marshal(actual)
					require.NoError(t, err)
					assert.NotContains(t, string(raw), "ORIGINAL")
				}
			}
			assert.Equal(t, int32(1), fixture.connections.Load())
			fixture.closeAndWait(t)
			assertResponsesWSAccounting(t, fixture, []int{10, 10})
		})
	}
}

func setupResponsesWSRequestTest(t *testing.T) (*model.User, *model.Token) {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousRDB := common.RDB
	previousSyncFrequency := common.SyncFrequency
	common.SyncFrequency = 60
	previousMaster, previousMemory, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.SQLitePath
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotal := setting.ModelRequestRateLimitCount
	previousSuccess := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitMutex.Lock()
	previousGroups := setting.ModelRequestRateLimitGroup
	setting.ModelRequestRateLimitGroup = nil
	setting.ModelRequestRateLimitMutex.Unlock()
	t.Setenv("SQL_DSN", os.Getenv("TEST_RESPONSES_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", os.Getenv("TEST_RESPONSES_LOG_SQL_DSN"))
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "responses.db")
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, model.InitLogDB())
	if model.LOG_DB != db {
		logSQL, err := model.LOG_DB.DB()
		require.NoError(t, err)
		logSQL.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
	}
	setting.ModelRequestRateLimitEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.RedisEnabled = previousRedis
		common.RDB = previousRDB
		common.SyncFrequency = previousSyncFrequency
		common.IsMasterNode, common.MemoryCacheEnabled, common.SQLitePath = previousMaster, previousMemory, previousSQLite
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount = previousTotal
		setting.ModelRequestRateLimitSuccessCount = previousSuccess
		setting.ModelRequestRateLimitMutex.Lock()
		setting.ModelRequestRateLimitGroup = previousGroups
		setting.ModelRequestRateLimitMutex.Unlock()
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	// The shared in-memory limiter outlives each database fixture. Give every
	// user a separate quota bucket, including when the tests run with -count.
	user := &model.User{Id: 5062000 + int(responsesWSTestUserSequence.Add(1)), Username: "responses-ws-user", Status: common.UserStatusEnabled, Group: "default", Quota: 1000, AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	token := &model.Token{UserId: user.Id, Key: "responseswstoken", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	require.NoError(t, db.Create(token).Error)
	t.Cleanup(func() {
		require.NoError(t, db.Unscoped().Delete(token).Error)
		require.NoError(t, db.Unscoped().Delete(user).Error)
	})
	return user, token
}

func newResponsesWSBillingTest(t *testing.T, expression string, handle func(*websocket.Conn, *http.Request), httpEvents ...string) *responsesWSBillingTest {
	t.Helper()
	user, token := setupResponsesWSRequestTest(t)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	previousBatch, previousLogs, previousCount, previousQuota := common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.CountToken, common.QuotaPerUnit
	common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.CountToken, common.QuotaPerUnit = false, true, false, 500000
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		switch key {
		case "billing_setting.billing_mode", "billing_setting.billing_expr", "group_ratio_setting.group_ratio", "perf_metrics_setting.enabled":
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.CountToken, common.QuotaPerUnit = previousBatch, previousLogs, previousCount, previousQuota
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	expressions, err := common.Marshal(map[string]string{"ws-billing": expression})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"ws-billing":"tiered_expr"}`,
		"billing_setting.billing_expr":    string(expressions),
		"group_ratio_setting.group_ratio": `{"default":1}`,
		"perf_metrics_setting.enabled":    "true",
	}))
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}, &model.PerfMetricError{}))
	require.NoError(t, model.DB.Model(user).Updates(map[string]any{"quota": 100000, "setting": `{"billing_preference":"wallet_only"}`}).Error)
	require.NoError(t, model.DB.Model(token).Update("remain_quota", 3000).Error)

	fixture := &responsesWSBillingTest{user: user, token: token, done: make(chan struct{}), upstreamDone: make(chan struct{}), httpDone: make(chan struct{}, 1)}
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.RDB, common.RedisEnabled = redisClient, true
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	var upstreamClosed sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			if fixture.httpUpstream != nil {
				fixture.httpUpstream(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range httpEvents {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		fixture.connections.Add(1)
		defer ws.Close()
		defer upstreamClosed.Do(func() { close(fixture.upstreamDone) })
		handle(ws, r)
	}))
	t.Cleanup(upstream.Close)
	channel := &model.Channel{Name: "responses-ws-upstream", Key: "upstream-first\nupstream-second", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, Group: "default", Models: "ws-billing", BaseURL: &upstream.URL,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}}
	modelMapping := `{"ws-billing":"gpt-4o"}`
	channel.ModelMapping = &modelMapping
	channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: true})
	channel.SetOtherSettings(dto.ChannelOtherSettings{AllowServiceTier: true})
	require.NoError(t, model.DB.Create(channel).Error)
	fixture.channel = channel
	require.NoError(t, model.DB.Create(&model.Ability{ChannelId: channel.Id, Model: "ws-billing", Group: "default", Enabled: true}).Error)
	t.Cleanup(func() {
		require.NoError(t, model.LOG_DB.Where("token_id = ?", token.Id).Delete(&model.Log{}).Error)
		require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Delete(channel).Error)
	})
	engine := gin.New()
	engine.GET("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) {
		defer close(fixture.done)
		c.Set(common.RequestIdKey, "responses-ws-billing")
		ResponsesWebSocket(c)
	})
	engine.POST("/v1/responses", middleware.TokenAuth(), middleware.ModelRequestRateLimit(), middleware.Distribute(), func(c *gin.Context) {
		defer func() { fixture.httpDone <- struct{}{} }()
		c.Set(common.RequestIdKey, "responses-http-billing")
		Relay(c, types.RelayFormatOpenAIResponses)
	})
	gateway := httptest.NewServer(engine)
	fixture.gatewayURL = gateway.URL
	t.Cleanup(gateway.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/responses", http.Header{"Authorization": []string{"Bearer sk-" + token.Key}})
	require.NoError(t, err)
	fixture.client = client
	t.Cleanup(func() { fixture.closeAndWait(t) })
	return fixture
}

func readResponsesWSTestEvent(t *testing.T, client *websocket.Conn) map[string]any {
	t.Helper()
	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, data, err := client.ReadMessage()
	require.NoError(t, err)
	var event map[string]any
	require.NoError(t, common.Unmarshal(data, &event))
	return event
}

func assertResponsesWSAccounting(t *testing.T, fixture *responsesWSBillingTest, expectedQuotas []int) {
	t.Helper()
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).Order("id").Find(&logs).Error)
	require.Len(t, logs, len(expectedQuotas))
	var charged int
	for index, quota := range expectedQuotas {
		assert.Equal(t, quota, logs[index].Quota)
		charged += quota
	}
	require.NoError(t, model.DB.First(fixture.token, fixture.token.Id).Error)
	require.NoError(t, model.DB.First(fixture.user, fixture.user.Id).Error)
	assert.Equal(t, 3000-charged, fixture.token.RemainQuota)
	assert.Equal(t, charged, fixture.token.UsedQuota)
	assert.Equal(t, 100000-charged, fixture.user.Quota)
	assert.Equal(t, charged, fixture.user.UsedQuota)
}

func TestResponsesWebSocketReusesConnectionAndSettlesEachRequest(t *testing.T) {
	type upstreamRequest struct {
		Authorization      string
		Type               string `json:"type"`
		Model              string `json:"model"`
		PreviousResponseID string `json:"previous_response_id"`
		ServiceTier        string `json:"service_tier"`
	}
	received := make(chan upstreamRequest, 3)
	fixture := newResponsesWSBillingTest(t, `param("service_tier") == "priority" ? tier("priority", p * 4) : tier("base", p * 2)`, func(ws *websocket.Conn, r *http.Request) {
		for index := 1; ; index++ {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var event upstreamRequest
			if !assert.NoError(t, common.Unmarshal(data, &event)) {
				return
			}
			event.Authorization = r.Header.Get("Authorization")
			received <- event
			if index == 2 {
				duplicate := `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1000,"output_tokens":10,"total_tokens":1010}}}`
				if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(duplicate))) {
					return
				}
			}
			terminal := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","status":"completed","model":"ws-billing","output":[],"usage":{"input_tokens":1000,"output_tokens":10,"total_tokens":1010}}}`, index)
			if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(terminal))) {
				return
			}
		}
	})
	client, user, token := fixture.client, fixture.user, fixture.token

	for index, payload := range []string{
		`{"type":"response.create","model":"ws-billing","input":"first","service_tier":"default"}`,
		`{"type":"response.create","model":"ws-billing","input":"second","previous_response_id":"resp_1","service_tier":"priority"}`,
	} {
		require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(payload)))
		require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
		_, data, err := client.ReadMessage()
		require.NoError(t, err)
		var terminal struct {
			Type     string `json:"type"`
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		require.NoError(t, common.Unmarshal(data, &terminal))
		require.Equal(t, "response.completed", terminal.Type, "unexpected response: %s", data)
		assert.Equal(t, fmt.Sprintf("resp_%d", index+1), terminal.Response.ID)
		observed := <-received
		assert.Equal(t, "Bearer upstream-first", observed.Authorization)
		assert.Equal(t, "gpt-4o", observed.Model)
		assert.Equal(t, "response.create", observed.Type)
		if index == 0 {
			assert.Empty(t, observed.PreviousResponseID)
			assert.Equal(t, "default", observed.ServiceTier)
		} else {
			assert.Equal(t, "resp_1", observed.PreviousResponseID)
			assert.Equal(t, "priority", observed.ServiceTier)
		}
	}

	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"exhausted"}`)))
	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, data, err := client.ReadMessage()
	require.NoError(t, err)
	var rejection struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	require.NoError(t, common.Unmarshal(data, &rejection))
	assert.Equal(t, "error", rejection.Type)
	assert.Equal(t, http.StatusUnauthorized, rejection.Status)
	var committed model.Token
	require.NoError(t, model.DB.First(&committed, token.Id).Error)
	require.Zero(t, committed.RemainQuota, "completed frames must finish token settlement before the next frame")
	cached, err := model.GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.Zero(t, cached.RemainQuota, "admission must observe settled quota")
	assert.Equal(t, int32(1), fixture.connections.Load())
	assert.Empty(t, received, "exhausted token must be rejected before contacting upstream")
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).Order("id").Find(&logs).Error)
	require.Len(t, logs, 2)
	for index, expectedQuota := range []int{1000, 2000} {
		assert.Equal(t, expectedQuota, logs[index].Quota)
		assert.Equal(t, fmt.Sprintf("responses-ws-billing-ws-%d", index), logs[index].RequestId)
		assert.Contains(t, logs[index].Other, `"terminal_write":"flushed"`)
		assert.Equal(t, 1000, logs[index].PromptTokens)
		assert.Equal(t, 10, logs[index].CompletionTokens)
	}
	for _, entry := range logs {
		assert.Equal(t, fixture.channel.Id, entry.ChannelId)
		var details struct {
			AdminInfo struct {
				UseChannel []string `json:"use_channel"`
			} `json:"admin_info"`
		}
		require.NoError(t, common.UnmarshalJsonStr(entry.Other, &details))
		assert.Equal(t, []string{fmt.Sprint(fixture.channel.Id)}, details.AdminInfo.UseChannel)
	}
	require.NoError(t, model.DB.First(token, token.Id).Error)
	require.NoError(t, model.DB.First(user, user.Id).Error)
	assert.Zero(t, token.RemainQuota)
	assert.Equal(t, 3000, token.UsedQuota)
	assert.Equal(t, 97000, user.Quota)
	assert.Equal(t, 3000, user.UsedQuota)
}

// Both transports must reach the same upstream target with the same
// credential placement and settle identically for every supported channel type.
func TestResponsesWebSocketInitialUpstreamRejectionRefundsReservation(t *testing.T) {
	for _, tc := range []struct {
		name, upstream, wantType, wantMessage string
		status                                int
	}{
		{name: "structured error", upstream: `{"type":"error","response_id":"rejected","status":400,"error":{"type":"invalid_request_error","code":"invalid_input","message":"Invalid input"}}`, status: http.StatusBadRequest, wantType: "invalid_request_error", wantMessage: "Invalid input"},
		// A frame without an error object is still reported as a request error.
		{name: "bare error", upstream: `{"type":"error","status":500,"message":"upstream rejected"}`, status: http.StatusInternalServerError, wantType: "invalid_request_error", wantMessage: "upstream rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preConsumed := make(chan int, 1)
			tokenID := make(chan int, 1)
			// Pre-consume no longer estimates completion tokens, so an output-priced expression reserves nothing to refund.
			fixture := newResponsesWSBillingTest(t, `tier("request", 2000)`, func(ws *websocket.Conn, _ *http.Request) {
				if _, _, err := ws.ReadMessage(); !assert.NoError(t, err) {
					return
				}
				var token model.Token
				if !assert.NoError(t, model.DB.First(&token, <-tokenID).Error) {
					return
				}
				preConsumed <- token.RemainQuota
				if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(tc.upstream))) {
					return
				}
				_, _, _ = ws.ReadMessage()
			})
			tokenID <- fixture.token.Id
			// Refund is asynchronous. Observe committed token writes instead of waiting
			// a fixed delay or returning while its worker still uses the test database.
			updates := make(chan struct{}, 4)
			require.NoError(t, model.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("responses-ws-refund", func(tx *gorm.DB) {
				if tx.Error == nil && tx.Statement.Table == "tokens" {
					select {
					case updates <- struct{}{}:
					default:
					}
				}
			}))
			require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"hi","max_output_tokens":10}`)))
			rejection := readResponsesWSTestEvent(t, fixture.client)
			assert.Equal(t, "error", rejection["type"])
			assert.Equal(t, float64(tc.status), rejection["status"])
			rejectionError, _ := rejection["error"].(map[string]any)
			assert.Equal(t, tc.wantType, rejectionError["type"])
			assert.Equal(t, tc.wantMessage, rejectionError["message"])
			select {
			case remaining := <-preConsumed:
				assert.Equal(t, 2000, remaining, "the rejected request reserved quota before contacting upstream")
			case <-time.After(3 * time.Second):
				t.Fatal("request did not reach upstream")
			}
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			for {
				require.NoError(t, model.DB.First(fixture.token, fixture.token.Id).Error)
				if fixture.token.RemainQuota == 3000 {
					break
				}
				select {
				case <-updates:
				case <-deadline.C:
					t.Fatal("initial rejection did not refund the token reservation")
				}
			}
			fixture.closeAndWait(t)
			assertResponsesWSAccounting(t, fixture, nil)
		})
	}
}

// TEST_RESPONSES_SQL_DSN / TEST_RESPONSES_LOG_SQL_DSN optionally run these
// entry-point regressions against isolated real MySQL/PostgreSQL databases.
func TestResponsesWebSocketDisconnectSettlesDeliveredOutputOnce(t *testing.T) {
	fixture := newResponsesWSBillingTest(t, `tier("output", c * 2)`, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); !assert.NoError(t, err) {
			return
		}
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"partial","status":"in_progress"}}`,
			`{"type":"response.output_text.delta","delta":"hello"}`,
		} {
			if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(event))) {
				return
			}
		}
		_, _, _ = ws.ReadMessage()
	})
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"hi","max_output_tokens":1}`)))
	assert.Equal(t, "response.created", readResponsesWSTestEvent(t, fixture.client)["type"])
	delta := readResponsesWSTestEvent(t, fixture.client)
	require.Equal(t, "response.output_text.delta", delta["type"])
	assert.Equal(t, "hello", delta["delta"])
	fixture.closeAndWait(t)
	assertResponsesWSAccounting(t, fixture, []int{1})
	var entry model.Log
	require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).First(&entry).Error)
	assert.Contains(t, entry.Other, "client_gone", "interrupted output keeps the actual delivery outcome")
}

func TestResponsesWebSocketCancelErrorDoesNotFinishActiveRequest(t *testing.T) {
	complete := make(chan struct{})
	defer close(complete)
	allowCreated := make(chan struct{}, 1)
	defer close(allowCreated)
	receivedCreate := make(chan struct{}, 1)
	fixture := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
		_, create, err := ws.ReadMessage()
		if !assert.NoError(t, err) || !assert.Contains(t, string(create), `"type":"response.create"`) {
			return
		}
		receivedCreate <- struct{}{}
		<-allowCreated
		if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"active","status":"in_progress"}}`))) {
			return
		}
		_, cancel, err := ws.ReadMessage()
		if !assert.NoError(t, err) || !assert.JSONEq(t, `{"type":"response.cancel","response_id":"wrong"}`, string(cancel)) {
			return
		}
		if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"response_not_found","message":"No such response"}}`))) {
			return
		}
		<-complete
		if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"active","status":"completed","usage":{"input_tokens":1000,"output_tokens":10,"total_tokens":1010}}}`))) {
			return
		}
		_, _, _ = ws.ReadMessage()
	})
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"hi"}`)))
	select {
	case <-receivedCreate:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not receive the initial request")
	}
	// Queue cancellation before the upstream accepts the response. The following
	// conflict is an acknowledgement that the client loop processed both frames.
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.cancel","response_id":"wrong"}`)))
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"before-acceptance"}`)))
	assert.Equal(t, float64(http.StatusConflict), readResponsesWSTestEvent(t, fixture.client)["status"])
	allowCreated <- struct{}{}
	assert.Equal(t, "response.created", readResponsesWSTestEvent(t, fixture.client)["type"])
	cancelError := readResponsesWSTestEvent(t, fixture.client)
	assert.Equal(t, "error", cancelError["type"])
	assert.Equal(t, float64(http.StatusBadRequest), cancelError["status"])
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"overlapping","stream_id":"other","event_id":"busy"}`)))
	conflict := readResponsesWSTestEvent(t, fixture.client)
	assert.Equal(t, "error", conflict["type"])
	assert.Equal(t, float64(http.StatusConflict), conflict["status"])
	assert.Equal(t, "other", conflict["stream_id"])
	assert.Equal(t, "busy", conflict["event_id"])
	complete <- struct{}{}
	assert.Equal(t, "response.completed", readResponsesWSTestEvent(t, fixture.client)["type"])
	fixture.closeAndWait(t)
	assertResponsesWSAccounting(t, fixture, []int{1000})
}

type responsesWSBillingTest struct {
	httpUpstream http.HandlerFunc
	httpDone     chan struct{}
	user         *model.User
	token        *model.Token
	channel      *model.Channel
	client       *websocket.Conn
	done         chan struct{}
	upstreamDone chan struct{}
	connections  atomic.Int32
	gatewayURL   string
}

func (fixture *responsesWSBillingTest) closeAndWait(t *testing.T) {
	t.Helper()
	_ = fixture.client.Close()
	select {
	case <-fixture.done:
	case <-time.After(3 * time.Second):
		t.Error("gateway handler did not stop after closing its client")
	}
	if fixture.connections.Load() > 0 {
		select {
		case <-fixture.upstreamDone:
		case <-time.After(3 * time.Second):
			t.Error("upstream connection was not closed")
		}
	}
	// Health classification is synchronous at the request boundary; only the
	// Redis write is asynchronous, see waitPerfCounters.
}
