package controller

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type upgradePipeWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *upgradePipeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestResponsesWebSocketShutdownInterruptsPendingClientUpgrade(t *testing.T) {
	manager := wsmanager.NewResponsesManager()
	router := gin.New()
	router.GET("/v1/responses", func(c *gin.Context) { serveResponsesWebSocket(c, manager) })
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	w := &upgradePipeWriter{ResponseRecorder: httptest.NewRecorder(), conn: a}
	r := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	done := make(chan struct{})
	go func() { defer close(done); router.ServeHTTP(w, r) }()
	// Leave the 101 write blocked after its first byte.
	require.NoError(t, b.SetReadDeadline(time.Now().Add(3*time.Second)))
	var prefix [1]byte
	_, err := b.Read(prefix[:])
	require.NoError(t, err)
	require.Equal(t, byte('H'), prefix[0])
	require.Equal(t, 1, manager.Stats().ClientHandshakes)
	manager.BeginDrain()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown left the 101 write blocked")
	}
	assert.Zero(t, manager.Stats().Current)
	assert.EqualValues(t, 1, manager.Stats().ReleaseReasons["shutdown"])
}

func TestResponsesWebSocketDrainsBilledTurnAndRejectsNewConnections(t *testing.T) {
	finish := make(chan struct{})
	next := sync.OnceFunc(func() { close(finish) })
	defer next()
	fixture := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(conn *websocket.Conn, _ *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"ws-drain","status":"in_progress"}}`)) != nil {
			return
		}
		<-finish
		if conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"ws-drain","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`)) != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
	})
	fixture.closeAndWait(t)
	manager := wsmanager.NewResponsesManager()
	cfg := manager.Settings()
	cfg.MaxUserConnections = 1
	manager.Configure(cfg)
	engine := gin.New()
	engine.GET("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) { serveResponsesWebSocket(c, manager) })
	server := httptest.NewServer(engine)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	headers := http.Header{"Authorization": {"Bearer sk-" + fixture.token.Key}}
	ws, _, err := websocket.DefaultDialer.Dial(url, headers)
	require.NoError(t, err)
	defer ws.Close()
	_, refused, err := websocket.DefaultDialer.Dial(url, headers)
	require.ErrorIs(t, err, websocket.ErrBadHandshake)
	require.NotNil(t, refused)
	defer refused.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, refused.StatusCode)
	require.Zero(t, fixture.connections.Load())
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"settle this turn"}`)))
	require.Equal(t, "response.created", readResponsesWSTestEvent(t, ws)["type"])
	drained := manager.BeginDrain()
	stats := manager.Stats()
	assert.Equal(t, 1, stats.Active)
	assert.Equal(t, 1, stats.UpstreamConnections)
	_, stopping, err := websocket.DefaultDialer.Dial(url, headers)
	require.ErrorIs(t, err, websocket.ErrBadHandshake)
	require.NotNil(t, stopping)
	defer stopping.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, stopping.StatusCode)
	next()
	require.Equal(t, "response.completed", readResponsesWSTestEvent(t, ws)["type"])
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err = ws.ReadMessage()
	require.True(t, websocket.IsCloseError(err, websocket.CloseGoingAway), "%v", err)
	for _, done := range []<-chan struct{}{drained, fixture.upstreamDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("connection remained open after terminal delivery")
		}
	}
	stats = manager.Stats()
	assert.Equal(t, map[string]uint64{"user": 1, "shutdown": 1}, stats.Rejections)
	assert.EqualValues(t, 1, stats.ReleaseReasons["shutdown"])
	assert.Zero(t, stats.Current)
	assert.Zero(t, stats.UpstreamConnections)
	assertResponsesWSAccounting(t, fixture, []int{10})
}

func TestResponsesWebSocketStatsContainsProcessCountsOnly(t *testing.T) {
	router := gin.New()
	router.GET("/stats", GetResponsesWebSocketStats)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var reply struct {
		Success bool
		Data    struct {
			Node        string
			Started     int64 `json:"started_at"`
			Connections wsmanager.ResponsesStats
		}
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &reply))
	require.True(t, reply.Success)
	assert.Equal(t, common.NodeName, reply.Data.Node)
	assert.Equal(t, common.StartTime, reply.Data.Started)
	assert.Equal(t, wsmanager.LocalResponses.Stats(), reply.Data.Connections)
}

func TestResponsesWebSocketIdleSettingsReleaseTheRightPeers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sendTurn bool
		reason   string
	}{
		{"first frame", false, "first_frame_timeout"},
		{"between settled turns", true, "idle_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := wsmanager.LocalResponses.Settings()
			t.Cleanup(func() { wsmanager.LocalResponses.Configure(before) })
			start := wsmanager.LocalResponses.Stats()
			cfg := before
			cfg.FirstFrameSeconds, cfg.IdleSeconds = 1, 1
			wsmanager.LocalResponses.Configure(cfg)
			f := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
				if ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"idle","status":"completed","output":[],"usage":{"input_tokens":10,"total_tokens":10}}}`)) != nil {
					return
				}
				_, _, _ = ws.ReadMessage()
			})
			if tc.sendTurn {
				require.NoError(t, f.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"idle next"}`)))
				require.Equal(t, "response.completed", readResponsesWSTestEvent(t, f.client)["type"])
			}
			require.NoError(t, f.client.SetReadDeadline(time.Now().Add(4*time.Second)))
			_, _, err := f.client.ReadMessage()
			require.True(t, websocket.IsCloseError(err, websocket.CloseGoingAway), "%v", err)
			select {
			case <-f.done:
			case <-time.After(3 * time.Second):
				t.Fatal("downstream did not release")
			}
			if tc.sendTurn {
				select {
				case <-f.upstreamDone:
				case <-time.After(3 * time.Second):
					t.Fatal("idle upstream did not release")
				}
				assertResponsesWSAccounting(t, f, []int{10})
			} else {
				assert.Zero(t, f.connections.Load())
				assertResponsesWSAccounting(t, f, nil)
			}
			end := wsmanager.LocalResponses.Stats()
			assert.Equal(t, start.ReleaseReasons[tc.reason]+1, end.ReleaseReasons[tc.reason])
			assert.Equal(t, start.Current, end.Current)
			assert.Equal(t, start.UpstreamConnections, end.UpstreamConnections)
		})
	}
}

func TestResponsesWebSocketChannelCapRefundsAndNextTurnCanUseReleasedSlot(t *testing.T) {
	before := wsmanager.LocalResponses.Settings()
	t.Cleanup(func() { wsmanager.LocalResponses.Configure(before) })
	cfg := before
	cfg.MaxChannelConnections = 1
	wsmanager.LocalResponses.Configure(cfg)
	f := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		if ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"after-limit","status":"completed","output":[],"usage":{"input_tokens":10,"total_tokens":10}}}`)) != nil {
			return
		}
		_, _, _ = ws.ReadMessage()
	})
	held, err := wsmanager.LocalResponses.Open(t.Context(), f.user.Id)
	require.NoError(t, err)
	defer held.Release()
	held.Upgraded()
	require.True(t, held.BeginTurn())
	require.NoError(t, held.BeginDial(f.channel.Id))
	held.FinishDial(true)
	held.EndTurn()
	frame := []byte(`{"type":"response.create","model":"ws-billing","input":"check quota"}`)
	require.NoError(t, f.client.WriteMessage(websocket.TextMessage, frame))
	event := readResponsesWSTestEvent(t, f.client)
	require.Equal(t, "error", event["type"])
	require.EqualValues(t, 429, event["status"])
	require.Zero(t, f.connections.Load())
	assertResponsesWSAccounting(t, f, nil)
	held.Release()
	require.NoError(t, f.client.WriteMessage(websocket.TextMessage, frame))
	require.Equal(t, "response.completed", readResponsesWSTestEvent(t, f.client)["type"])
	f.closeAndWait(t)
	assertResponsesWSAccounting(t, f, []int{10})
}

func TestResponsesWebSocketFullChannelUsesAvailablePeerAndBillsOncePerTurn(t *testing.T) {
	previous := wsmanager.LocalResponses.Settings()
	cfg := previous
	cfg.MaxChannelConnections = 1
	wsmanager.LocalResponses.Configure(cfg)
	t.Cleanup(func() { wsmanager.LocalResponses.Configure(previous) })
	oldRetries := common.RetryTimes
	common.RetryTimes = 0
	t.Cleanup(func() { common.RetryTimes = oldRetries })

	f := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, req *http.Request) {
		assert.Equal(t, "Bearer available-peer", req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("OpenAI-Organization"))
		for turn := 1; ; turn++ {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
			terminal := fmt.Sprintf(`{"type":"response.completed","response":{"id":"from-peer-%d","status":"completed","output":[],"usage":{"input_tokens":10,"total_tokens":10}}}`, turn)
			if ws.WriteMessage(websocket.TextMessage, []byte(terminal)) != nil {
				return
			}
		}
	})
	peer := *f.channel
	peer.Id, peer.Name, peer.Key = 0, "available WS channel", "available-peer"
	peer.ChannelInfo = model.ChannelInfo{}
	peer.Priority = common.GetPointer(int64(-1))
	require.NoError(t, model.DB.Create(&peer).Error)
	require.NoError(t, model.DB.Model(f.channel).Update("OpenAIOrganization", "primary-only").Error)
	require.NoError(t, model.DB.Create(&model.Ability{ChannelId: peer.Id, Model: "ws-billing", Group: "default", Enabled: true, Priority: peer.Priority}).Error)
	held, err := wsmanager.LocalResponses.Open(t.Context(), f.user.Id)
	require.NoError(t, err)
	defer held.Release()
	held.Upgraded()
	require.True(t, held.BeginTurn())
	require.NoError(t, held.BeginDial(f.channel.Id))
	held.FinishDial(true)
	held.EndTurn()

	frame := []byte(`{"type":"response.create","model":"ws-billing","input":"local turn"}`)
	require.NoError(t, f.client.WriteMessage(websocket.TextMessage, frame))
	require.EqualValues(t, 429, readResponsesWSTestEvent(t, f.client)["status"])
	assert.Zero(t, f.connections.Load())
	assertResponsesWSAccounting(t, f, nil)
	common.RetryTimes = 1
	for turn := 0; turn < 2; turn++ {
		require.NoError(t, f.client.WriteMessage(websocket.TextMessage, frame))
		event := readResponsesWSTestEvent(t, f.client)
		require.Equal(t, "response.completed", event["type"], "turn %d: %v", turn, event)
		held.Release() // The second turn must retain its already-established connection.
	}
	f.closeAndWait(t)
	assert.EqualValues(t, 1, f.connections.Load())
	assertResponsesWSAccounting(t, f, []int{10, 10})
	var channels []int
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Order("id").Pluck("channel_id", &channels).Error)
	assert.Equal(t, []int{peer.Id, peer.Id}, channels)
}

func TestResponsesWebSocketMessageLimitAppliesThroughTurnMiddleware(t *testing.T) {
	previous := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	cfg := wsmanager.LocalResponses.Settings()
	t.Cleanup(func() {
		constant.MaxRequestBodyMB = previous
		wsmanager.LocalResponses.Configure(cfg)
	})
	wsSettings := cfg
	wsSettings.ClientMessageMiB = 2
	wsmanager.LocalResponses.Configure(wsSettings)
	f := newResponsesWSBillingTest(t, `tier("base", p * 2)`, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		if ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"size-ok","status":"completed","output":[],"usage":{"input_tokens":2,"total_tokens":2}}}`)) != nil {
			return
		}
		_, _, _ = ws.ReadMessage()
	})
	req := map[string]any{"model": "ws-billing", "input": "hi", "metadata": map[string]string{"test_padding": strings.Repeat("a", 1<<20)}}
	httpBody, err := common.Marshal(req)
	require.NoError(t, err)
	// Network request headers cannot opt an ordinary HTTP request into the WS limit.
	probe, err := http.NewRequest(http.MethodPost, f.gatewayURL+"/v1/responses", bytes.NewReader(httpBody))
	require.NoError(t, err)
	probe.Header.Set("Authorization", "Bearer sk-"+f.token.Key)
	probe.Header.Set("Content-Type", "application/json")
	probe.Header.Set("X-Request-Body-Limit-MiB", "2")
	response, err := http.DefaultClient.Do(probe)
	require.NoError(t, err)
	defer response.Body.Close()
	require.GreaterOrEqual(t, response.StatusCode, http.StatusBadRequest)
	reply, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(reply), "request body exceeds 1 MB")
	req["type"] = "response.create"
	frame, err := common.Marshal(req)
	require.NoError(t, err)
	require.NoError(t, f.client.WriteMessage(websocket.TextMessage, frame))
	require.Equal(t, "response.completed", readResponsesWSTestEvent(t, f.client)["type"])
	f.closeAndWait(t)
	assertResponsesWSAccounting(t, f, []int{2})
}
