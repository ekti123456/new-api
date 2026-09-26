package relay

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCPAIdentitySignedFinalWire(t *testing.T) {
	const secret = "test-only-cpa-identity-signing-secret-20260926"
	captured := make(chan cpaCapturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		captured <- cpaCapturedRequest{r.URL.RequestURI(), r.Header.Clone(), b}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	t.Setenv("CPA_IDENTITY_TARGETS", upstream.URL+"/v1")
	t.Setenv("CPA_IDENTITY_INSTANCE_ID", "persistent-newapi-test-instance")
	t.Setenv("CPA_IDENTITY_SIGNING_SECRET", secret)
	var wire []map[string]any
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, passthrough := range []bool{false, true} {
			c, info, request := cpaRequestFixture(t, path, `{"model":"gpt-5.1","input":"hello","client_metadata":{"session_id":"same-client-session"}}`, upstream.URL, passthrough)
			info.UserId, info.TokenId, info.RequestId = 101, 7, "newapi-request-1"
			common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{"X-CPA-Identity": "forged", "X-CPA-Identity-Signature": "forged"})
			adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
			require.Nil(t, apiErr)
			resp, err := adaptor.DoRequest(c, info, body)
			require.NoError(t, err)
			require.NoError(t, closer.Close())
			require.NoError(t, resp.(*http.Response).Body.Close())
			got := <-captured
			raw := got.header.Get("X-CPA-Identity")
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			require.NoError(t, err)
			assert.Equal(t, "101", gjson.GetBytes(decoded, "user").String())
			assert.Equal(t, "7", gjson.GetBytes(decoded, "key").String())
			assert.Equal(t, "Codex Desktop/test", gjson.GetBytes(decoded, "ua").String())
			assert.Equal(t, "client-installation", gjson.GetBytes(decoded, "device").String())
			digest := sha256.Sum256(got.body)
			assert.Equal(t, hex.EncodeToString(digest[:]), gjson.GetBytes(decoded, "body_sha256").String())
			mac := hmac.New(sha256.New, []byte(secret))
			_, err = mac.Write([]byte(strings.Join([]string{"cpa-identity-v1", "POST", got.path, raw}, "\n")))
			require.NoError(t, err)
			assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), got.header.Get("X-CPA-Identity-Signature"))
			wire = append(wire, map[string]any{"path": got.path, "body": string(got.body), "headers": got.header})
		}
	}
	if path := os.Getenv("CPA_IDENTITY_WIRE_CAPTURE"); path != "" {
		b, err := common.Marshal(wire)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, b, 0600))
	}
	// A retry outside the configured CPA destination must not leak assertions,
	// even if a channel override explicitly attempts to inject them.
	t.Setenv("CPA_IDENTITY_TARGETS", "https://not-this-destination.example/v1")
	c, info, request := cpaRequestFixture(t, "/v1/responses", `{"model":"gpt-5.1","input":"hi"}`, upstream.URL, true)
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{"X-CPA-Identity": "forged", "X-CPA-Identity-Signature": "forged"})
	adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
	require.Nil(t, apiErr)
	resp, err := adaptor.DoRequest(c, info, body)
	require.NoError(t, err)
	require.NoError(t, closer.Close())
	require.NoError(t, resp.(*http.Response).Body.Close())
	got := <-captured
	assert.Empty(t, got.header.Get("X-CPA-Identity"))
	assert.Empty(t, got.header.Get("X-CPA-Identity-Signature"))
	t.Setenv("CPA_IDENTITY_TARGETS", upstream.URL)
	t.Setenv("CPA_IDENTITY_SIGNING_SECRET", "")
	adaptor, body, closer, apiErr = PrepareResponsesRequest(c, info, request)
	require.Nil(t, apiErr)
	_, err = adaptor.DoRequest(c, info, body)
	require.ErrorContains(t, err, "signing secret")
	require.NoError(t, closer.Close())
}

type cpaCapturedRequest struct {
	path   string
	header http.Header
	body   []byte
}

func TestCPAIdentityWebSocketHandshake(t *testing.T) {
	const secret = "test-only-cpa-identity-signing-secret-20260926"
	captured := make(chan cpaCapturedRequest, 1)
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		captured <- cpaCapturedRequest{r.URL.RequestURI(), r.Header.Clone(), nil}
	}))
	defer upstream.Close()
	t.Setenv("CPA_IDENTITY_TARGETS", upstream.URL+"/v1")
	t.Setenv("CPA_IDENTITY_INSTANCE_ID", "persistent-newapi-test-instance")
	t.Setenv("CPA_IDENTITY_SIGNING_SECRET", secret)
	c, info, request := cpaRequestFixture(t, "/v1/responses", `{"model":"gpt-5.1","input":"hi"}`, upstream.URL, false)
	info.UserId, info.TokenId, info.RequestId = 101, 7, "websocket-handshake"
	c.Request.Method = http.MethodGet
	adaptor, _, closer, apiErr := PrepareResponsesRequest(c, info, request)
	require.Nil(t, apiErr)
	defer closer.Close()
	conn, err := channel.DoWssRequest(adaptor, c, info, nil)
	require.NoError(t, err)
	defer conn.Close()
	got := <-captured
	raw := got.header.Get("X-CPA-Identity")
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	assert.Equal(t, "101", gjson.GetBytes(decoded, "user").String())
	emptyHash := sha256.Sum256(nil)
	assert.Equal(t, hex.EncodeToString(emptyHash[:]), gjson.GetBytes(decoded, "body_sha256").String())
	mac := hmac.New(sha256.New, []byte(secret))
	_, err = mac.Write([]byte(strings.Join([]string{"cpa-identity-v1", "GET", got.path, raw}, "\n")))
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), got.header.Get("X-CPA-Identity-Signature"))
}

func cpaRequestFixture(t *testing.T, path, payload, upstream string, passthrough bool) (*gin.Context, *relaycommon.RelayInfo, *dto.OpenAIResponsesRequest) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer downstream-only-key")
	c.Request.Header.Set("Cookie", "session=downstream-only")
	c.Request.Header.Set("Chatgpt-Account-Id", "downstream-only-account")
	c.Request.Header.Set("X-Api-Key", "downstream-only-key")
	c.Request.Header.Set("User-Agent", "Codex Desktop/test")
	c.Request.Header.Set("Originator", "Codex Desktop")
	c.Request.Header.Set("Version", "test-version")
	c.Request.Header.Set("X-Codex-Window-Id", "client-window:0")
	c.Request.Header.Set("X-Codex-Installation-Id", "client-installation")
	c.Request.Header.Set("X-Codex-Turn-Metadata", `{"request_kind":"turn","turn_id":"client-turn"}`)
	c.Request.Header.Set("X-Codex-Turn-State", "client-state")
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(payload), &request))
	common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "cpa-channel-key")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, storage.Close()) })
	info := relaycommon.GenRelayInfoResponses(c, &request)
	info.DisablePing = true
	return c, info, &request
}

func TestCPAResponsesRequestWire(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previousPassthrough := global.PassThroughRequestEnabled
	global.PassThroughRequestEnabled = false
	t.Cleanup(func() { global.PassThroughRequestEnabled = previousPassthrough })
	affinity := operation_setting.GetChannelAffinitySetting()
	previousAffinity := affinity.Enabled
	affinity.Enabled = false
	t.Cleanup(func() { affinity.Enabled = previousAffinity })
	captured := make(chan cpaCapturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		captured <- cpaCapturedRequest{r.URL.Path, r.Header.Clone(), body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeNewAPI, constant.ChannelTypeSub2API} {
		for _, passthrough := range []bool{false, true} {
			for _, tc := range []struct{ name, extension string }{
				{"prewarm", `,"generate":false,"access_programs":{"cyber":"standard"},"client_metadata":{"session_id":"client-session","turn_id":"client-turn"},"reasoning":{"effort":"xhigh"}`},
				{"generate", `,"generate":true,"client_metadata":{"session_id":"client-session"},"reasoning":{"effort":"none"}`},
				{"no-session-or-effort", ""},
			} {
				t.Run(fmt.Sprintf("channel=%d/passthrough=%t/%s", channelType, passthrough, tc.name), func(t *testing.T) {
					payload := `{"model":"gpt-5.1","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}],"opaque_number":9007199254740993}],"tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"text"}}]}],"store":false,"service_tier":"priority","prompt_cache_key":"client-cache","context_management":[{"type":"compaction","compact_threshold":200000}]` + tc.extension + `}`
					c, info, request := cpaRequestFixture(t, "/v1/responses", payload, upstream.URL, passthrough)
					common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
					if tc.name != "no-session-or-effort" {
						c.Request.Header.Set("Session_id", "client-session")
					}
					adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
					require.Nil(t, apiErr)
					defer closer.Close()
					response, err := adaptor.DoRequest(c, info, body)
					require.NoError(t, err)
					resp := response.(*http.Response)
					defer resp.Body.Close()
					require.Equal(t, http.StatusNoContent, resp.StatusCode)
					got := <-captured
					assert.Equal(t, "/v1/responses", got.path)
					if passthrough {
						assert.Equal(t, payload, string(got.body))
					}
					for _, field := range []string{"input", "tools", "store", "client_metadata", "generate", "access_programs", "prompt_cache_key", "context_management", "reasoning"} {
						want := gjson.Get(payload, field)
						actual := gjson.GetBytes(got.body, field)
						assert.Equal(t, want.Exists(), actual.Exists(), field)
						assert.Equal(t, want.Raw, actual.Raw, field)
					}
					assert.Equal(t, "9007199254740993", gjson.GetBytes(got.body, "input.0.opaque_number").Raw)
					assert.Equal(t, passthrough, gjson.GetBytes(got.body, "service_tier").Exists(), "channel's existing field policy still applies")
					for _, name := range []string{"User-Agent", "Originator", "Version", "Session_id", "X-Codex-Window-Id", "X-Codex-Installation-Id", "X-Codex-Turn-Metadata", "X-Codex-Turn-State"} {
						assert.Equal(t, c.Request.Header.Get(name), got.header.Get(name), name)
					}
					assert.Equal(t, "Bearer cpa-channel-key", got.header.Get("Authorization"))
					for _, name := range []string{"Cookie", "Chatgpt-Account-Id", "X-Api-Key"} {
						assert.Empty(t, got.header.Get(name), name)
					}
				})
			}
		}
	}
}

func TestCPAResponsesRetryRebuildsChannelHeaders(t *testing.T) {
	captured := make(chan cpaCapturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		captured <- cpaCapturedRequest{r.URL.Path, r.Header.Clone(), body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	const payload = `{"model":"gpt-5.1","input":"hello","client_metadata":{"session_id":"original-session"},"generate":false}`
	c, info, request := cpaRequestFixture(t, "/v1/responses", payload, upstream.URL, false)
	// An opaque client value must never be expanded as a channel-key template.
	c.Request.Header.Set("X-Codex-Turn-State", "{api_key}")
	for attempt := range 3 {
		common.SetContextKey(c, constant.ContextKeyChannelKey, fmt.Sprintf("channel-key-%d", attempt))
		common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{})
		common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{})
		common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: attempt == 2})
		if attempt == 0 {
			common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{"user-agent": "configured-agent"})
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []map[string]any{
				{"mode": "set_header", "path": "Authorization", "value": "Bearer first-channel-only"},
				{"mode": "set_header", "path": "X-First-Channel", "value": "first-only"},
				{"mode": "delete_header", "path": "X-Codex-Window-Id"},
			}})
		}
		adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
		require.Nil(t, apiErr)
		response, err := adaptor.DoRequest(c, info, body)
		require.NoError(t, err)
		require.NoError(t, response.(*http.Response).Body.Close())
		require.NoError(t, closer.Close())
		got := <-captured
		assert.Equal(t, "{api_key}", got.header.Get("X-Codex-Turn-State"))
		assert.JSONEq(t, payload, string(got.body))
		if attempt == 0 {
			assert.Equal(t, "Bearer first-channel-only", got.header.Get("Authorization"))
			assert.Equal(t, "configured-agent", got.header.Get("User-Agent"))
			assert.Equal(t, "first-only", got.header.Get("X-First-Channel"))
			assert.Empty(t, got.header.Get("X-Codex-Window-Id"))
		} else {
			assert.Equal(t, fmt.Sprintf("Bearer channel-key-%d", attempt), got.header.Get("Authorization"))
			assert.Equal(t, "Codex Desktop/test", got.header.Get("User-Agent"))
			assert.Empty(t, got.header.Get("X-First-Channel"))
			assert.Equal(t, "client-window:0", got.header.Get("X-Codex-Window-Id"))
		}
	}
}

func TestCPACompactionAndPassthroughErrors(t *testing.T) {
	captured := make(chan cpaCapturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured <- cpaCapturedRequest{r.URL.Path, r.Header.Clone(), body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"fixture capacity exhausted","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	}))
	defer upstream.Close()
	global := model_setting.GetGlobalSettings()
	previous := global.PassThroughRequestEnabled
	t.Cleanup(func() { global.PassThroughRequestEnabled = previous })
	for _, mode := range []string{"converted", "channel-passthrough", "global-passthrough"} {
		t.Run(mode, func(t *testing.T) {
			global.PassThroughRequestEnabled = mode == "global-passthrough"
			payload := `{"model":"gpt-5.1","input":[{"type":"compaction","encrypted_content":"opaque_compaction_state"}],"instructions":"summarize","prompt_cache_key":"original-cache","client_metadata":{"session_id":"original-session"},"vendor_extension":{"counter":9007199254740993}}`
			c, _, _ := cpaRequestFixture(t, "/v1/responses/compact", payload, upstream.URL, mode == "channel-passthrough")
			var request dto.OpenAIResponsesCompactionRequest
			require.NoError(t, common.Unmarshal([]byte(payload), &request))
			info := relaycommon.GenRelayInfoResponsesCompaction(c, &request)
			apiErr := ResponsesHelper(c, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			assert.Contains(t, apiErr.Error(), "fixture capacity exhausted")
			got := <-captured
			assert.Equal(t, "/v1/responses/compact", got.path)
			assert.Equal(t, "client-state", got.header.Get("X-Codex-Turn-State"))
			assert.Equal(t, "client-window:0", got.header.Get("X-Codex-Window-Id"))
			assert.Equal(t, "Bearer cpa-channel-key", got.header.Get("Authorization"))
			assert.Equal(t, "original-cache", gjson.GetBytes(got.body, "prompt_cache_key").String())
			assert.Equal(t, "opaque_compaction_state", gjson.GetBytes(got.body, "input.0.encrypted_content").String())
			if mode != "converted" {
				assert.Equal(t, payload, string(got.body), "passthrough retains all compact extension fields")
			}
		})
	}
}

func TestCPAResponsesStreamFlushesBeforeTerminal(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	if previousTimeout <= 0 {
		constant.StreamingTimeout = 30
	}
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	terminal := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(terminal) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("OpenAI-Model", "gpt-5.1")
		w.Header().Set("X-Reasoning-Included", "")
		w.Header().Set("X-Models-Etag", "models-test")
		w.Header().Set("X-Codex-Turn-State", "next-state")
		w.Header().Set("Set-Cookie", "upstream-private=secret")
		w.Header().Set("Chatgpt-Account-Id", "upstream-private-account")
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.1\",\"status\":\"in_progress\"}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-terminal:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n")
	}))
	defer upstream.Close()
	defer release()
	result := make(chan error, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		request := &dto.OpenAIResponsesRequest{Model: "gpt-5.1", Input: []byte(`"hello"`), Stream: common.GetPointer(true)}
		common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
		common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
		info := relaycommon.GenRelayInfoResponses(c, request)
		info.DisablePing = true
		adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
		if apiErr != nil {
			result <- apiErr
			http.Error(w, apiErr.Error(), 500)
			return
		}
		defer closer.Close()
		response, err := adaptor.DoRequest(c, info, body)
		if err != nil {
			result <- err
			http.Error(w, err.Error(), 500)
			return
		}
		_, apiErr = adaptor.DoResponse(c, response.(*http.Response), info)
		if apiErr != nil {
			result <- apiErr
			return
		}
		result <- nil
	}))
	defer downstream.Close()
	defer release()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(downstream.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5.1","input":"hello","stream":true}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "gpt-5.1", resp.Header.Get("OpenAI-Model"))
	assert.Equal(t, []string{""}, resp.Header.Values("X-Reasoning-Included"))
	assert.Equal(t, "models-test", resp.Header.Get("X-Models-Etag"))
	assert.Equal(t, "next-state", resp.Header.Get("X-Codex-Turn-State"))
	assert.Empty(t, resp.Header.Get("Set-Cookie"))
	assert.Empty(t, resp.Header.Get("Chatgpt-Account-Id"))
	scanner := bufio.NewScanner(resp.Body)
	var received strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		received.WriteString(line + "\n")
		if strings.HasPrefix(line, "data:") {
			break
		}
	}
	require.NoError(t, scanner.Err())
	require.Contains(t, received.String(), "response.created", "must arrive while upstream is waiting to send the terminal event")
	release()
	for scanner.Scan() {
		received.WriteString(scanner.Text() + "\n")
	}
	require.NoError(t, scanner.Err())
	assert.Contains(t, received.String(), "response.completed")
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish")
	}
}
