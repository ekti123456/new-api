package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSavedPolicySettingsOverrideEnvironmentWithoutRestart(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	prior := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = prior; common.OptionMapRWMutex.Unlock() })
	t.Setenv("CODEX2API_POLICY_ENABLED", "false")
	t.Setenv("CODEX2API_POLICY_BINDINGS", `[{"target":"https://codex.example","platform_id":"gateway-a","codex_key_fingerprint":"`+strings.Repeat("a", 64)+`","secret":"`+strings.Repeat("s", 32)+`","enabled":true}]`)
	s, source, revision, err := ReadNewAPIPolicySettings()
	require.NoError(t, err)
	assert.Equal(t, "environment", source)
	require.Len(t, s.Bindings, 1)
	assert.False(t, s.Enabled)
	s.Enabled = true
	raw, err := common.Marshal(s)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[NewAPIPolicyOption] = string(raw)
	common.OptionMapRWMutex.Unlock()
	runtime, err := loadNewAPIPolicyConfig()
	require.NoError(t, err)
	assert.True(t, runtime.Enabled)
	_, source, next, err := ReadNewAPIPolicySettings()
	require.NoError(t, err)
	assert.Equal(t, "database", source)
	assert.NotEqual(t, revision, next)
	s.IdentityForwardEnabled = false
	raw, err = common.Marshal(s)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[NewAPIPolicyOption] = string(raw)
	common.OptionMapRWMutex.Unlock()
	runtime, err = loadNewAPIPolicyConfig()
	require.NoError(t, err)
	assert.False(t, runtime.Enabled)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[NewAPIPolicyOption] = "broken"
	common.OptionMapRWMutex.Unlock()
	_, err = loadNewAPIPolicyConfig()
	require.Error(t, err)
}

func TestPolicyProbeValidatesSignedResponseAndNeverFollowsRedirects(t *testing.T) {
	key := "sk-test-probe"
	secret := strings.Repeat("s", 32)
	digest := sha256.Sum256([]byte(key))
	mode := "success"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/prompt-filter/newapi/verify", r.URL.Path)
		assert.Equal(t, "Bearer "+key, r.Header.Get("Authorization"))
		id, ts := r.Header.Get("X-NewAPI-Request-ID"), r.Header.Get("X-NewAPI-Timestamp")
		assert.Equal(t, newAPIHMAC(secret, strings.Join([]string{"v1", ts, id, "1", "127.0.0.1", "POST", r.URL.Path, r.Header.Get("X-NewAPI-Body-SHA256")}, "\n")), r.Header.Get("X-NewAPI-Signature"))
		if mode == "denied" {
			w.WriteHeader(401)
			return
		}
		sig := newAPIHMAC(secret, strings.Join([]string{"newapi-handshake-result-v1", id, "gateway-a", ts, "ok"}, "\n"))
		if mode == "forged" {
			sig = "forged"
		}
		if mode == "legacy" {
			sig = ""
		}
		body, err := common.Marshal(map[string]any{"success": true, "request_id": id, "platform": "gateway-a", "policy_meta_verified": true, "handshake_signature": sig})
		require.NoError(t, err)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	b := NewAPIPolicyBinding{Target: server.URL, PlatformID: "gateway-a", Secret: secret, CodexKeyFingerprint: hex.EncodeToString(digest[:]), Enabled: true}
	for _, tc := range []struct{ mode, state string }{{"success", "connected"}, {"denied", "authentication_failed"}, {"forged", "invalid_response"}, {"legacy", "upgrade_required"}} {
		mode = tc.mode
		assert.Equal(t, tc.state, ProbeNewAPIPolicy(context.Background(), b, key).State)
	}
	mode = "success"
	b.Target = server.URL + "/v1"
	assert.Equal(t, "connected", ProbeNewAPIPolicy(context.Background(), b, key).State)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, 302) }))
	defer redirect.Close()
	b.Target = redirect.URL
	assert.Equal(t, "upstream_error", ProbeNewAPIPolicy(context.Background(), b, key).State)
}

func TestAuditProbeUsesOnlyTheBoundChannelKeyAndDestination(t *testing.T) {
	key := "sk-bound-test"
	digest := sha256.Sum256([]byte(key))
	binding := NewAPIPolicyBinding{Target: "https://codex.example/proxy/v1", CodexKeyFingerprint: hex.EncodeToString(digest[:])}
	for _, tc := range []struct {
		name, base, key string
		matches         bool
	}{
		{"base_without_version", "https://codex.example/proxy", key, true},
		{"base_with_version", "https://codex.example/proxy/v1", key, true},
		{"different_key", "https://codex.example/proxy", "sk-other", false},
		{"different_host", "https://other.example/proxy", key, false},
		{"different_path", "https://codex.example/proxy-other", key, false},
		{"different_port", "https://codex.example:8443/proxy", key, false},
		{"credentials_in_url", "https://user:password@codex.example/proxy", key, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.matches, NewAPIPolicyBindingMatchesChannel(binding, tc.base, tc.key))
		})
	}
}
