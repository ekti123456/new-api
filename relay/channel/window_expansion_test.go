package channel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWindowControlUsesUniqueSignedRequestIDsAndDoesNotReuseGrant(test *testing.T) {
	requests := make(chan http.Header, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Header.Clone()
		_, _ = io.WriteString(writer, `{"version":1,"windows":[]}`)
	}))
	defer server.Close()
	binding := newAPIPolicyBinding{PlatformID: "window-test", Target: server.URL, Secret: "0123456789abcdef0123456789abcdef", Enabled: true}
	digest := sha256.Sum256([]byte("test-key"))
	binding.CodexKeyFingerprint = hex.EncodeToString(digest[:])
	configurePolicyTest(test, []newAPIPolicyBinding{binding})
	request, _ := gin.CreateTestContext(httptest.NewRecorder())
	request.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Request.RemoteAddr = "203.0.113.9:1234"
	info := &relaycommon.RelayInfo{UserId: 42, RequestId: "original-relay", WindowBilling: &relaycommon.WindowBillingGrant{Ticket: "must-not-forward-to-control"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "test-key"}}
	for range 2 {
		_, err := RequestUserWindows(request, info, WindowControlInput{Operation: "list", Multiplier: 1})
		require.NoError(test, err)
	}
	first, second := <-requests, <-requests
	require.NotEmpty(test, first.Get("X-NewAPI-Signature"))
	require.NotEqual(test, "original-relay", first.Get("X-NewAPI-Request-ID"))
	require.NotEqual(test, first.Get("X-NewAPI-Request-ID"), second.Get("X-NewAPI-Request-ID"))
	require.Equal(test, "Bearer test-key", first.Get("Authorization"))
	metadata, err := base64.RawURLEncoding.DecodeString(first.Get("X-NewAPI-Policy-Meta"))
	require.NoError(test, err)
	var meta newAPIPolicyMeta
	require.NoError(test, common.Unmarshal(metadata, &meta))
	require.Empty(test, meta.WindowGrant)
	require.Equal(test, "original-relay", info.RequestId)
}

func TestWindowBillingWarmRootAvoidsControlRequestAndFailureInvalidatesCache(test *testing.T) {
	binding := newAPIPolicyBinding{PlatformID: "window-test", Target: "http://127.0.0.1:18995", Secret: "0123456789abcdef0123456789abcdef", Enabled: true}
	digest := sha256.Sum256([]byte("test-key"))
	binding.CodexKeyFingerprint = hex.EncodeToString(digest[:])
	configurePolicyTest(test, []newAPIPolicyBinding{binding})
	request, _ := gin.CreateTestContext(httptest.NewRecorder())
	request.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rootID := "11111111-1111-4111-8111-111111111111"
	request.Request.Header.Set("Session-Id", rootID)
	common.SetContextKey(request, constant.ContextKeyChannelId, 7)
	common.SetContextKey(request, constant.ContextKeyChannelBaseUrl, binding.Target)
	common.SetContextKey(request, constant.ContextKeyChannelKey, "test-key")
	info := &relaycommon.RelayInfo{UserId: 42, UserSetting: dto.UserSetting{WindowExpansionJoined: true}}
	fingerprint := newAPIPolicyRootSessionFingerprint(binding.PlatformID, "42", rootID)
	cacheKey := windowBindingHash(binding, "test-key") + ":42:" + fingerprint
	windowBillingCache.Lock()
	windowBillingCache.putLocked(cacheKey, cachedWindowGrant{grant: relaycommon.WindowBillingGrant{ID: "confirmed", Expanded: true, Multiplier: 1.5, ExpiresAt: time.Now().Add(time.Hour)}, until: time.Now().Add(time.Hour)})
	windowBillingCache.Unlock()
	test.Cleanup(func() {
		windowBillingCache.Lock()
		windowBillingCache.deleteLocked(cacheKey)
		windowBillingCache.Unlock()
	})
	finish, err := PrepareWindowBilling(request, info)
	require.NoError(test, err)
	require.NotNil(test, finish)
	require.NotNil(test, info.WindowBilling)
	require.Equal(test, 1.5, info.WindowMultiplier())
	require.Nil(test, info.ChannelMeta)
	finish(false)
	windowBillingCache.Lock()
	_, found := windowBillingCache.items[cacheKey]
	windowBillingCache.Unlock()
	require.False(test, found)
}

func TestWindowBillingCacheEvictsColdRootWithoutFlushingActiveRoots(test *testing.T) {
	cache := newWindowGrantCache(2)
	cache.putLocked("active", cachedWindowGrant{})
	cache.putLocked("cold", cachedWindowGrant{})
	_, _ = cache.getLocked("active")
	cache.putLocked("new", cachedWindowGrant{})
	_, active := cache.getLocked("active")
	_, cold := cache.getLocked("cold")
	require.True(test, active)
	require.False(test, cold)
	require.Len(test, cache.items, 2)
	cache.putLocked("active", cachedWindowGrant{})
	require.Len(test, cache.items, 2)
	cache.deleteLocked("active")
	require.Len(test, cache.items, 1)
	require.Equal(test, 1, cache.order.Len())
}

func TestWindowTicketRejectsAnotherUserRootBindingAndTampering(test *testing.T) {
	binding := newAPIPolicyBinding{PlatformID: "window-test", Target: "http://127.0.0.1", Secret: "0123456789abcdef0123456789abcdef", Enabled: true}
	encoded, err := common.Marshal(map[string]interface{}{
		"version": 1, "platform": binding.PlatformID, "user_id": "42", "root_fingerprint": "root",
		"grant": map[string]interface{}{"id": "window", "root": "hash", "expires_at": time.Now().Add(time.Hour), "expanded": true, "multiplier": 1.5},
	})
	require.NoError(test, err)
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	ticket := payload + "." + newAPIHMAC(binding.Secret, "codex2api-window-grant-v1\n"+payload)
	grant, err := verifyWindowBillingTicket(binding, "key", 42, "root", ticket)
	require.NoError(test, err)
	require.Equal(test, 1.5, grant.Multiplier)
	_, err = verifyWindowBillingTicket(binding, "key", 43, "root", ticket)
	require.Error(test, err)
	_, err = verifyWindowBillingTicket(binding, "key", 42, "another-root", ticket)
	require.Error(test, err)
	_, err = verifyWindowBillingTicket(binding, "key", 42, "root", ticket+"tampered")
	require.Error(test, err)
	info := &relaycommon.RelayInfo{WindowBilling: grant, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "other-key"}}
	require.Error(test, ValidateWindowBillingDestination(info, binding, "root"))
}
