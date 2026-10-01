package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type NewAPIPolicyProbeResult struct {
	State      string    `json:"state"`
	CheckedAt  time.Time `json:"checked_at"`
	LatencyMS  int64     `json:"latency_ms"`
	HTTPStatus int       `json:"http_status,omitempty"`
}

// Match the channel's Responses destination, including bases with a /v1 or
// reverse-proxy prefix. A key is never reused across hosts or binding scopes.
func NewAPIPolicyBindingMatchesChannel(b NewAPIPolicyBinding, baseURL, key string) bool {
	u, err := parsePolicyTarget(baseURL)
	if err != nil || b.CodexKeyFingerprint == "" {
		return false
	}
	prefix := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(prefix, "/v1") {
		prefix += "/v1"
	}
	u.Path, u.RawPath = prefix+"/responses", ""
	if !policyTargetMatches(b.Target, u) {
		return false
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hmac.Equal([]byte(b.CodexKeyFingerprint), []byte(hex.EncodeToString(digest[:])))
}

// The handshake exercises the real API-key and signed-identity checks without
// model inference. Passwords, keys and upstream response bodies are never returned.
func ProbeNewAPIPolicy(ctx context.Context, b NewAPIPolicyBinding, key string) NewAPIPolicyProbeResult {
	start := time.Now()
	result := NewAPIPolicyProbeResult{State: "invalid_configuration", CheckedAt: start.UTC()}
	u, err := parsePolicyTarget(b.Target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || normalizeNewAPIPolicyBinding(&b) != nil {
		return result
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(key)))
	if key == "" || (b.CodexKeyFingerprint != "" && hex.EncodeToString(digest[:]) != b.CodexKeyFingerprint) {
		result.State = "channel_key_missing"
		return result
	}
	// Accept either the origin or an API base ending in /v1, including a reverse-proxy prefix.
	prefix := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(prefix, "/v1") {
		prefix += "/v1"
	}
	u.Path = prefix + "/prompt-filter/newapi/verify"
	u.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return result
	}
	id := common.NewRequestId()
	ts := strconv.FormatInt(start.Unix(), 10)
	empty := sha256.Sum256(nil)
	bodyDigest := hex.EncodeToString(empty[:])
	path := u.EscapedPath()
	fields := []string{"v1", ts, id, "1", "127.0.0.1", http.MethodPost, path, bodyDigest}
	meta, _ := common.Marshal(newAPIPolicyMeta{PlatformID: b.PlatformID, Profile: b.Profile, Mode: b.Mode, Provider: "codex2api", Protocol: "openai", OriginalEndpoint: "/v1/prompt-filter/newapi/verify"})
	encoded := base64.RawURLEncoding.EncodeToString(meta)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-NewAPI-User-ID", "1")
	req.Header.Set("X-NewAPI-Client-IP", "127.0.0.1")
	req.Header.Set("X-NewAPI-Request-ID", id)
	req.Header.Set("X-NewAPI-Timestamp", ts)
	req.Header.Set("X-NewAPI-Method", http.MethodPost)
	req.Header.Set("X-NewAPI-Path", path)
	req.Header.Set("X-NewAPI-Body-SHA256", bodyDigest)
	req.Header.Set("X-NewAPI-Signature-Version", newAPISignatureVersion)
	req.Header.Set("X-NewAPI-Signature", newAPIHMAC(b.Secret, strings.Join(fields, "\n")))
	req.Header.Set("X-NewAPI-Policy-Meta", encoded)
	req.Header.Set("X-NewAPI-Policy-Meta-Signature", newAPIHMAC(b.Secret, strings.Join([]string{newAPIPolicyMetaVersion, id, bodyDigest, encoded}, "\n")))
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.State = "network_error"
		return result
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	switch resp.StatusCode {
	case 401, 403:
		result.State = "authentication_failed"
		return result
	case 404, 405:
		result.State = "endpoint_unavailable"
		return result
	case 422:
		result.State = "identity_mismatch"
		return result
	case 429:
		result.State = "rate_limited"
		return result
	case 200:
	default:
		result.State = "upstream_error"
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(body) > 65536 {
		result.State = "invalid_response"
		return result
	}
	var response struct {
		Success      bool   `json:"success"`
		Platform     string `json:"platform"`
		RequestID    string `json:"request_id"`
		MetaVerified bool   `json:"policy_meta_verified"`
		Signature    string `json:"handshake_signature"`
	}
	if common.Unmarshal(body, &response) != nil || !response.Success || response.Platform != b.PlatformID || response.RequestID != id || !response.MetaVerified {
		result.State = "invalid_response"
		return result
	}
	if response.Signature == "" {
		result.State = "upgrade_required"
		return result
	}
	expected := newAPIHMAC(b.Secret, strings.Join([]string{"newapi-handshake-result-v1", id, b.PlatformID, ts, "ok"}, "\n"))
	if !hmac.Equal([]byte(expected), []byte(response.Signature)) {
		result.State = "invalid_response"
		return result
	}
	result.State = "connected"
	return result
}
