package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type windowReloadFixture struct {
	binding           newAPIPolicyBinding
	settings          NewAPIPolicySettings
	grant             relaycommon.WindowBillingGrant
	root, fingerprint string
	releases          chan WindowControlInput
	inferenceStarted  chan struct{}
	inferenceContinue chan struct{}
}

func (f *windowReloadFixture) ticket(grant relaycommon.WindowBillingGrant, reservation, owner, fingerprint string) string {
	raw, _ := common.Marshal(map[string]any{"version": 1, "platform": f.binding.PlatformID, "user_id": owner, "root_fingerprint": fingerprint, "reservation_id": reservation, "grant": grant})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + newAPIHMAC(f.binding.Secret, "codex2api-window-grant-v1\n"+payload)
}

func newWindowReloadFixture(t *testing.T) *windowReloadFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "windows.db")), &gorm.Config{})
	require.NoError(t, err)
	previousDB, previousRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		InvalidateUserWindowBilling(42)
		model.DB, common.RedisEnabled = previousDB, previousRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		connection, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, connection.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}))
	require.NoError(t, db.Create(&model.User{Id: 42, Username: "window-reload", Setting: `{"window_expansion_joined":true}`}).Error)
	InvalidateUserWindowBilling(42)
	f := &windowReloadFixture{binding: newAPIPolicyBinding{PlatformID: "original-platform", Secret: strings.Repeat("a", 32), Enabled: true}, root: "11111111-1111-4111-8111-111111111111", releases: make(chan WindowControlInput, 16)}
	digest := sha256.Sum256([]byte("test-key"))
	f.binding.CodexKeyFingerprint = hex.EncodeToString(digest[:])
	f.fingerprint = newAPIPolicyRootSessionFingerprint(f.binding.PlatformID, "42", f.root)
	f.grant = relaycommon.WindowBillingGrant{ID: "original-grant", Root: "original-window", Multiplier: 1, ExpiresAt: time.Now().Add(time.Hour).UTC(), PendingUntil: time.Now().Add(30 * time.Second).UTC()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			w.WriteHeader(500)
			return
		}
		digest := sha256.Sum256(body)
		canonical := strings.Join([]string{"v1", r.Header.Get("X-NewAPI-Timestamp"), r.Header.Get("X-NewAPI-Request-ID"), "42", r.Header.Get("X-NewAPI-Client-IP"), "POST", r.URL.Path, hex.EncodeToString(digest[:])}, "\n")
		if !assert.Equal(t, newAPIHMAC(f.binding.Secret, canonical), r.Header.Get("X-NewAPI-Signature")) || !assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization")) {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/v1/responses" && f.inferenceStarted != nil {
			encoded, err := base64.RawURLEncoding.DecodeString(r.Header.Get("X-NewAPI-Policy-Meta"))
			if !assert.NoError(t, err) {
				w.WriteHeader(400)
				return
			}
			var meta newAPIPolicyMeta
			if !assert.NoError(t, common.Unmarshal(encoded, &meta)) {
				w.WriteHeader(400)
				return
			}
			payload, _, _ := strings.Cut(meta.WindowGrant, ".")
			raw, err := base64.RawURLEncoding.DecodeString(payload)
			if !assert.NoError(t, err) {
				w.WriteHeader(400)
				return
			}
			var reservation struct {
				ID string `json:"reservation_id"`
			}
			if !assert.NoError(t, common.Unmarshal(raw, &reservation)) {
				w.WriteHeader(400)
				return
			}
			close(f.inferenceStarted)
			<-f.inferenceContinue
			confirmed := f.grant
			confirmed.Confirmed = true
			w.Header().Set("X-Codex2API-Window-Grant", f.ticket(confirmed, reservation.ID, "42", f.fingerprint))
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
			return
		}
		var input WindowControlInput
		if !assert.NoError(t, common.Unmarshal(body, &input)) {
			w.WriteHeader(400)
			return
		}
		if input.Operation == "release" {
			f.releases <- input
			_, _ = io.WriteString(w, `{"version":1}`)
			return
		}
		if input.Operation != "quote" && input.Operation != "quote_tiered" {
			w.WriteHeader(400)
			return
		}
		payload, _ := common.Marshal(WindowControlResult{Version: 1, Ticket: f.ticket(f.grant, input.ReservationID, "42", f.fingerprint)})
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	f.binding.Target = server.URL
	configurePolicyTest(t, []newAPIPolicyBinding{f.binding})
	f.settings, _, _, err = ReadNewAPIPolicySettings()
	require.NoError(t, err)
	return f
}

type windowReloadAdaptor struct {
	Adaptor
	target string
}

func (a windowReloadAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return a.target + "/v1/responses", nil
}
func (a windowReloadAdaptor) SetupRequestHeader(_ *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	h.Set("Authorization", "Bearer "+info.ApiKey)
	h.Set("Content-Type", "application/json")
	return nil
}

func TestWindowHotReloadDuringHTTPResponsePreservesStream(t *testing.T) {
	f := newWindowReloadFixture(t)
	f.inferenceStarted = make(chan struct{})
	f.inferenceContinue = make(chan struct{})
	t.Cleanup(func() { close(f.inferenceContinue) })
	c, info := f.request()
	finish, err := PrepareWindowBilling(c, info)
	require.NoError(t, err)
	require.NotNil(t, finish)
	info.InitChannelMeta(c)
	type result struct {
		response *http.Response
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		response, err := DoApiRequest(windowReloadAdaptor{target: f.binding.Target}, c, info, strings.NewReader(`{"input":"hello","stream":true}`))
		completed <- result{response: response, err: err}
	}()
	select {
	case <-f.inferenceStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("inference request did not reach the mock upstream")
	}
	f.settings.Enabled = false
	f.save(t)
	f.inferenceContinue <- struct{}{}
	var outcome result
	select {
	case outcome = <-completed:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream response did not complete")
	}
	require.NoError(t, outcome.err)
	require.NotNil(t, outcome.response)
	defer outcome.response.Body.Close()
	require.Equal(t, http.StatusOK, outcome.response.StatusCode)
	require.Empty(t, outcome.response.Header.Get("X-Codex2API-Window-Grant"))
	body, err := io.ReadAll(outcome.response.Body)
	require.NoError(t, err)
	require.Equal(t, "data: {\"type\":\"response.completed\"}\n\n", string(body))
	require.True(t, info.WindowBilling.Confirmed)
	finish(true)
	select {
	case <-f.releases:
		t.Fatal("confirmed window must not be released")
	default:
	}
}

func (f *windowReloadFixture) request() (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.RemoteAddr = "203.0.113.9:1234"
	c.Request.Header.Set("Session-Id", f.root)
	common.SetContextKey(c, constant.ContextKeyChannelId, 7)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, f.binding.Target)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
	return c, &relaycommon.RelayInfo{UserId: 42, UserSetting: dto.UserSetting{WindowExpansionJoined: true}}
}

func (f *windowReloadFixture) save(t *testing.T) {
	t.Helper()
	require.NoError(t, f.settings.Normalize())
	raw, err := common.Marshal(f.settings)
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{NewAPIPolicyOption: string(raw)}))
}

func TestWindowHotReloadConfirmationAndRelease(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*NewAPIPolicySettings)
	}{
		{"disabled", func(s *NewAPIPolicySettings) { s.Enabled = false }},
		{"identity_disabled", func(s *NewAPIPolicySettings) { s.IdentityForwardEnabled = false }},
		{"binding_disabled", func(s *NewAPIPolicySettings) { s.Bindings[0].Enabled = false }},
		{"removed", func(s *NewAPIPolicySettings) { s.Bindings = nil }},
		{"secret", func(s *NewAPIPolicySettings) { s.Bindings[0].Secret = strings.Repeat("b", 32) }},
		{"platform", func(s *NewAPIPolicySettings) { s.Bindings[0].PlatformID = "new-platform" }},
		{"equivalent_target", func(s *NewAPIPolicySettings) { s.Bindings[0].Target += "/v1" }},
		{"different_target", func(s *NewAPIPolicySettings) { s.Bindings[0].Target = "https://unused.invalid" }},
		{"key_match", func(s *NewAPIPolicySettings) {
			digest := sha256.Sum256([]byte("other-key"))
			s.Bindings[0].CodexKeyFingerprint = hex.EncodeToString(digest[:])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWindowReloadFixture(t)
			c, info := f.request()
			finish, err := PrepareWindowBilling(c, info)
			require.NoError(t, err)
			require.NotNil(t, finish)
			pendingC, pendingInfo := f.request()
			pendingFinish, err := PrepareWindowBilling(pendingC, pendingInfo)
			require.NoError(t, err)
			require.NotNil(t, pendingFinish)
			info.InitChannelMeta(c)
			tc.edit(&f.settings)
			f.save(t)
			// Authorization already started. Its payload and confirmation keep one
			// connection snapshot even if settings change before the payload write.
			outbound := httptest.NewRequest(http.MethodPost, f.binding.Target+"/v1/responses", strings.NewReader(`{"input":"hello"}`))
			require.NoError(t, applyNewAPIPolicyHeaders(c, outbound, info, strings.NewReader(`{"input":"hello"}`)))
			require.NotEmpty(t, outbound.Header.Get("X-NewAPI-Signature"))
			confirmed := f.grant
			confirmed.Confirmed = true
			response := &http.Response{Header: make(http.Header)}
			response.Header.Set("X-Codex2API-Window-Grant", f.ticket(confirmed, info.WindowBilling.ReservationID, "42", f.fingerprint))
			require.NoError(t, acceptWindowAuthorizationResponse(c, response, info))
			require.True(t, info.WindowBilling.Confirmed)
			require.Equal(t, f.grant.Root, info.WindowBilling.Root)
			require.Equal(t, f.grant.ExpiresAt, info.WindowBilling.ExpiresAt)
			require.Equal(t, float64(1), info.WindowMultiplier())
			require.Empty(t, response.Header.Get("X-Codex2API-Window-Grant"))
			finish(true)
			pendingFinish(false)
			select {
			case released := <-f.releases:
				require.Equal(t, pendingInfo.WindowBilling.ReservationID, released.ReservationID)
				require.Equal(t, f.grant.ID, released.GrantID)
			default:
				t.Fatal("unconfirmed reservation was not released on its original connection")
			}
			// A fresh request cannot inherit the old request's connection snapshot.
			fresh, freshInfo := f.request()
			freshInfo.InitChannelMeta(fresh)
			cfg, err := loadNewAPIPolicyConfig()
			require.NoError(t, err)
			next := httptest.NewRequest(http.MethodPost, f.binding.Target+"/v1/responses", nil)
			require.NoError(t, applyNewAPIPolicyHeaders(fresh, next, freshInfo, nil))
			binding, matched := matchNewAPIPolicyBinding(cfg.Bindings, next.URL, freshInfo.ApiKey)
			if !cfg.Enabled || !matched {
				require.Empty(t, next.Header.Get("X-NewAPI-Signature"))
			} else {
				metaBytes, err := base64.RawURLEncoding.DecodeString(next.Header.Get("X-NewAPI-Policy-Meta"))
				require.NoError(t, err)
				var meta newAPIPolicyMeta
				require.NoError(t, common.Unmarshal(metaBytes, &meta))
				require.Equal(t, binding.PlatformID, meta.PlatformID)
				canonical := strings.Join([]string{"v1", next.Header.Get("X-NewAPI-Timestamp"), next.Header.Get("X-NewAPI-Request-ID"), "42", next.Header.Get("X-NewAPI-Client-IP"), "POST", next.URL.Path, next.Header.Get("X-NewAPI-Body-SHA256")}, "\n")
				require.Equal(t, newAPIHMAC(binding.Secret, canonical), next.Header.Get("X-NewAPI-Signature"))
			}
		})
	}
}

func TestWindowHotReloadRefreshStaysOnAuthorizedConnection(t *testing.T) {
	f := newWindowReloadFixture(t)
	c, info := f.request()
	_, err := PrepareWindowBilling(c, info)
	require.NoError(t, err)
	original := *info.WindowBilling
	f.settings.Enabled = false
	f.save(t)
	finish, err := RefreshWindowBilling(c, info)
	require.NoError(t, err)
	require.NotNil(t, finish)
	require.Equal(t, original.BindingHash, info.WindowBilling.BindingHash)
	require.Equal(t, original.Fingerprint, info.WindowBilling.Fingerprint)
	require.NotEqual(t, original.ReservationID, info.WindowBilling.ReservationID)
	select {
	case released := <-f.releases:
		require.Equal(t, original.ReservationID, released.ReservationID)
	default:
		t.Fatal("old reservation not released before renewal")
	}
	finish(false)
	fresh, freshInfo := f.request()
	freshInfo.InitChannelMeta(fresh)
	_, err = RequestUserWindows(fresh, freshInfo, WindowControlInput{Operation: "list", Multiplier: 1})
	require.ErrorContains(t, err, "unavailable")
}

func TestWindowHotReloadRejectsCrossScopeAndTamperedConfirmation(t *testing.T) {
	for _, field := range []string{"user", "key", "channel", "destination", "signature", "root", "tariff", "expiry", "owner"} {
		t.Run(field, func(t *testing.T) {
			f := newWindowReloadFixture(t)
			c, info := f.request()
			_, err := PrepareWindowBilling(c, info)
			require.NoError(t, err)
			info.InitChannelMeta(c)
			f.settings.Enabled = false
			f.save(t)
			confirmed := f.grant
			confirmed.Confirmed = true
			owner, fingerprint := "42", f.fingerprint
			switch field {
			case "user":
				info.UserId++
			case "key":
				info.ApiKey = "wrong-key"
			case "channel":
				info.ChannelId++
			case "destination":
				info.ChannelBaseUrl = "https://unused.invalid"
			case "root":
				fingerprint = "wrong-root"
			case "tariff":
				confirmed.Expanded = true
				confirmed.Multiplier = 2
			case "expiry":
				confirmed.ExpiresAt = confirmed.ExpiresAt.Add(time.Hour)
			case "owner":
				owner = strconv.Itoa(info.UserId + 1)
			}
			ticket := f.ticket(confirmed, info.WindowBilling.ReservationID, owner, fingerprint)
			if field == "signature" {
				ticket += "tampered"
			}
			response := &http.Response{Header: make(http.Header)}
			response.Header.Set("X-Codex2API-Window-Grant", ticket)
			require.Error(t, acceptWindowAuthorizationResponse(c, response, info))
			require.False(t, info.WindowBilling.Confirmed)
			require.Empty(t, response.Header.Get("X-Codex2API-Window-Grant"))
			if field == "user" || field == "key" || field == "channel" || field == "destination" {
				out := httptest.NewRequest(http.MethodPost, info.ChannelBaseUrl+"/v1/responses", bytes.NewReader(nil))
				require.Error(t, applyNewAPIPolicyHeaders(c, out, info, bytes.NewReader(nil)))
				require.Empty(t, out.Header.Get("X-NewAPI-Signature"))
			}
		})
	}
}
