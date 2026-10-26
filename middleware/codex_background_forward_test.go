package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

type backgroundRootLookupObserver struct{ calls atomic.Int32 }

func (o *backgroundRootLookupObserver) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	for _, arg := range cmd.Args() {
		key, ok := arg.(string)
		if ok && (strings.Contains(key, "codex_root") || strings.Contains(key, "codex_turn") || strings.Contains(key, "codex_prefix") || strings.Contains(key, "codex_passive")) {
			o.calls.Add(1)
		}
	}
	return ctx, nil
}
func (*backgroundRootLookupObserver) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (o *backgroundRootLookupObserver) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		_, _ = o.BeforeProcess(ctx, cmd)
	}
	return ctx, nil
}
func (*backgroundRootLookupObserver) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestCodexBackgroundForwardsWithoutLocalRootState(t *testing.T) {
	for backendIndex, backend := range []string{"memory", "redis"} {
		for sourceIndex, source := range []string{"thread_title", "thread_description", "thread_title_reconsideration", "thread_summary", "ambient_suggestions", "guardian_review", "subagent", "agent_created_thread", "system", "memory_consolidation"} {
			t.Run(backend+"/"+source, func(t *testing.T) {
				ch, key, fingerprint := setupCodexRootDistributorTest(t)
				var queries atomic.Int32
				require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("test:background-query-count", func(*gorm.DB) { queries.Add(1) }))
				var observer *backgroundRootLookupObserver
				if backend == "redis" {
					_, client := useCodexRecentRootRedisFixture(t, time.Now())
					observer = &backgroundRootLookupObserver{}
					client.AddHook(observer)
				}
				user, token := 995000+backendIndex*100+sourceIndex, 995099
				const root = "01a17841-91b3-7373-977f-8e0cf8a738e0"
				// Even a stale local route must not stop the background request.
				require.NoError(t, service.StoreCodexRootChannelBinding(user, root, service.CodexRootChannelBinding{ChannelID: ch.Id + 1000, SelectedGroup: "pro", KeyFingerprint: fingerprint}))
				c, rec := codexUnlinkedNativeTitleContext(user, token, root)
				metadata := strings.ReplaceAll(c.GetHeader("X-Codex-Turn-Metadata"), "thread_title", source)
				body := []byte(`{"model":"gpt-5.4","input":"background work","client_metadata":` + metadata + `}`)
				c.Request.Header.Set("X-Codex-Turn-Metadata", metadata)
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
				c.Request.ContentLength = int64(len(body))
				common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
				common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-5.6-sol": true})
				_, background := codexUserConcurrencyPolicy(c)
				require.True(t, background)
				require.True(t, relaychannel.CodexRootAccountWaitDeadline(c).IsZero(), "NewAPI must not start the upstream root wait budget")
				if observer != nil {
					observer.calls.Store(0)
				}
				queries.Store(0)
				Distribute()(c)
				assert.False(t, c.IsAborted(), rec.Body.String())
				assert.Equal(t, ch.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
				assert.Equal(t, key, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
				assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyCodexRootChannelPinned))
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyCodexBackgroundDelegated))
				assert.True(t, service.ShouldSkipRetryAfterChannelAffinityFailure(c))
				assert.Empty(t, relaychannel.CodexPassiveRootSessionOverrideFeature(c))
				assert.Equal(t, root, relaychannel.ResolveCodexRootSessionForDistribution(c).RootID)
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				out, err := storage.Bytes()
				require.NoError(t, err)
				assert.Equal(t, body, out)
				if observer != nil {
					assert.Zero(t, observer.calls.Load(), "no legacy root lookup or publication on the request path")
				}
				assert.Zero(t, queries.Load(), "cached routing must not add per-request DB queries")
				common.CleanupBodyStorage(c)
			})
		}
	}
}

func TestCodexBackgroundDelegationRequiresConfiguredDestination(t *testing.T) {
	for _, scenario := range []string{"ordinary_user", "unbound_destination", "other_group", "disabled", "multiple_destinations"} {
		t.Run(scenario, func(t *testing.T) {
			ch, key, _ := setupCodexRootDistributorTest(t)
			c, rec := codexUnlinkedNativeTitleContext(995301, 995302, "01a17841-0000-7000-8000-000000000301")
			switch scenario {
			case "ordinary_user":
				c.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(c.GetHeader("X-Codex-Turn-Metadata"), "thread_title", "user"))
			case "unbound_destination":
				t.Setenv("CODEX2API_POLICY_BINDINGS", "[]")
			case "other_group":
				require.NoError(t, model.DB.Model(ch).Update("group", "other").Error)
				require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", ch.Id).Update("group", "other").Error)
			case "disabled":
				require.NoError(t, model.DB.Model(ch).Update("status", common.ChannelStatusManuallyDisabled).Error)
			case "multiple_destinations":
				base := ch.GetBaseURL()
				require.NoError(t, model.DB.Create(&model.Channel{Id: ch.Id + 1, Type: ch.Type, Key: key, BaseURL: &base, Status: common.ChannelStatusEnabled, Group: "pro", Models: "gpt-5.6-sol"}).Error)
			}
			model.InitChannelCache()
			common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-5.6-sol": true})
			Distribute()(c)
			assert.True(t, c.IsAborted())
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyCodexBackgroundDelegated))
			assert.Zero(t, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
			common.CleanupBodyStorage(c)
		})
	}
}

type backgroundHTTPAdaptor struct {
	relaychannel.Adaptor
	target string
}

func (a backgroundHTTPAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return a.target + "/v1/responses", nil
}
func (a backgroundHTTPAdaptor) SetupRequestHeader(_ *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	header.Set("Authorization", "Bearer "+info.ApiKey)
	header.Set("Content-Type", "application/json")
	return nil
}

func TestCodexBackgroundHTTPForwardsSignedIdentityAndUpstreamDecision(t *testing.T) {
	ch, key, fingerprint := setupCodexRootDistributorTest(t)
	type capturedRequest struct {
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 1)
	upstreamBody := `{"error":{"code":"codex_background_root_unavailable","message":"upstream owns the session decision"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		captured <- capturedRequest{r.Header.Clone(), body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, upstreamBody)
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, model.DB.Model(ch).Update("base_url", upstream.URL).Error)
	model.InitChannelCache()
	const secret = "0123456789abcdef0123456789abcdef"
	t.Setenv("CODEX2API_POLICY_BINDINGS", fmt.Sprintf(`[{"platform_id":"newapi","target":%q,"codex_key_fingerprint":%q,"secret":%q,"enabled":true}]`, upstream.URL, fingerprint, secret))
	const root = "01a17841-91b3-7373-977f-8e0cf8a738e0"
	c, rec := codexUnlinkedNativeTitleContext(995401, 995402, root)
	c.Request.Header.Set("X-NewAPI-User-ID", "spoofed-user")
	c.Request.RemoteAddr = "203.0.113.9:1234"
	c.Set(common.RequestIdKey, "background-forward-test")
	Distribute()(c)
	require.False(t, c.IsAborted(), rec.Body.String())
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	body, err := storage.Bytes()
	require.NoError(t, err)
	var input dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(body, &input))
	info := &relaycommon.RelayInfo{UserId: 995401, TokenId: 995402, UserGroup: "pro", RequestId: "background-forward-test", OriginModelName: input.Model,
		RelayFormat: types.RelayFormatOpenAIResponses, Request: &input,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: ch.Id, ApiKey: key, UpstreamModelName: input.Model}}
	resp, err := relaychannel.DoApiRequest(backgroundHTTPAdaptor{target: upstream.URL}, c, info, bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	reply, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, upstreamBody, string(reply))
	wire := <-captured
	assert.Equal(t, body, wire.body)
	assert.Equal(t, "995401", wire.header.Get("X-NewAPI-User-ID"))
	assert.Equal(t, "Bearer "+key, wire.header.Get("Authorization"))
	digest := sha256.Sum256(body)
	canonical := "v1\n" + wire.header.Get("X-NewAPI-Timestamp") + "\nbackground-forward-test\n995401\n203.0.113.9\nPOST\n/v1/responses\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), wire.header.Get("X-NewAPI-Signature"))
	meta, err := base64.RawURLEncoding.DecodeString(wire.header.Get("X-NewAPI-Policy-Meta"))
	require.NoError(t, err)
	assert.Equal(t, root, gjson.GetBytes(meta, "root_session_id").String())
	assert.Equal(t, "thread_title", gjson.GetBytes(meta, "thread_source").String())
	assert.False(t, gjson.GetBytes(meta, "root_account_wait_millis").Exists())
	assert.Empty(t, captured)
	common.CleanupBodyStorage(c)
}

func TestCodexBackgroundWebSocketForwardsIdentityAndUpstreamError(t *testing.T) {
	ch, key, fingerprint := setupCodexRootDistributorTest(t)
	captured := make(chan http.Header, 1)
	upstreamError := `{"type":"error","error":{"code":"upstream_session_unavailable","message":"upstream owns the session decision"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		captured <- r.Header.Clone()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(upstreamError))
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, model.DB.Model(ch).Updates(map[string]any{"base_url": upstream.URL, "setting": `{"responses_websocket_enabled":true}`}).Error)
	model.InitChannelCache()
	const secret = "0123456789abcdef0123456789abcdef"
	t.Setenv("CODEX2API_POLICY_BINDINGS", fmt.Sprintf(`[{"platform_id":"newapi","target":%q,"codex_key_fingerprint":%q,"secret":%q,"enabled":true}]`, upstream.URL, fingerprint, secret))
	const root = "01a17841-91b3-7373-977f-8e0cf8a738e0"
	c, rec := codexUnlinkedNativeTitleContext(995501, 995502, root)
	c.Set("responses_websocket", true)
	c.Set("responses_websocket_pin", ResponsesWSConnectionPin{ChannelID: ch.Id, Key: key})
	c.Set(common.RequestIdKey, "background-websocket-test")
	c.Request.RemoteAddr = "203.0.113.9:1234"
	Distribute()(c)
	require.False(t, c.IsAborted(), rec.Body.String())
	var input dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalBodyReusable(c, &input))
	info := &relaycommon.RelayInfo{UserId: 995501, TokenId: 995502, UserGroup: "pro", RequestId: "background-websocket-test", OriginModelName: input.Model,
		RelayFormat: types.RelayFormatOpenAIResponses, Request: &input,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: ch.Id, ApiKey: key, UpstreamModelName: input.Model}}
	conn, err := relaychannel.DoWssRequest(backgroundHTTPAdaptor{target: upstream.URL}, c, info, nil)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	kind, reply, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, kind)
	assert.Equal(t, upstreamError, string(reply))
	wire := <-captured
	assert.Equal(t, "995501", wire.Get("X-NewAPI-User-ID"))
	assert.Equal(t, "Bearer "+key, wire.Get("Authorization"))
	digest := sha256.Sum256(nil)
	canonical := "v1\n" + wire.Get("X-NewAPI-Timestamp") + "\nbackground-websocket-test\n995501\n203.0.113.9\nGET\n/v1/responses\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), wire.Get("X-NewAPI-Signature"))
	meta, err := base64.RawURLEncoding.DecodeString(wire.Get("X-NewAPI-Policy-Meta"))
	require.NoError(t, err)
	assert.Equal(t, root, gjson.GetBytes(meta, "root_session_id").String())
	assert.Equal(t, "thread_title", gjson.GetBytes(meta, "thread_source").String())
	common.CleanupBodyStorage(c)
}
