package channel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const (
	newAPISignatureVersion  = "1"
	newAPIPolicyMetaVersion = "policy-meta-v1"
)

var newAPIPolicyHeaderNames = []string{
	"X-NewAPI-User-ID",
	"X-NewAPI-Client-IP",
	"X-NewAPI-Request-ID",
	"X-NewAPI-Timestamp",
	"X-NewAPI-Method",
	"X-NewAPI-Path",
	"X-NewAPI-Body-SHA256",
	"X-NewAPI-Signature-Version",
	"X-NewAPI-Signature",
	"X-NewAPI-Policy-Meta",
	"X-NewAPI-Policy-Meta-Signature",
}

type newAPIPolicyBinding struct {
	PlatformID          string `json:"platform_id"`
	Target              string `json:"target"`
	CodexKeyFingerprint string `json:"codex_key_fingerprint"`
	Secret              string `json:"secret"`
	Enabled             bool   `json:"enabled"`
	Profile             string `json:"profile,omitempty"`
	Mode                string `json:"mode,omitempty"`
}

type newAPIPolicyConfig struct {
	Enabled  bool
	Bindings []newAPIPolicyBinding
}

type newAPIPolicyMeta struct {
	PlatformID       string `json:"platform_id"`
	UserName         string `json:"user_name,omitempty"`
	UserEmail        string `json:"user_email,omitempty"`
	UserGroup        string `json:"user_group,omitempty"`
	Profile          string `json:"profile"`
	Mode             string `json:"mode"`
	Provider         string `json:"provider"`
	Protocol         string `json:"protocol"`
	OriginalEndpoint string `json:"original_endpoint,omitempty"`
	OriginalProtocol string `json:"original_protocol,omitempty"`
	RequestedModel   string `json:"requested_model,omitempty"`
	UpstreamModel    string `json:"upstream_model,omitempty"`
	ChannelID        int    `json:"channel_id,omitempty"`
}

func applyNewAPIPolicyHeaders(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo, requestBody io.Reader) error {
	if req == nil {
		return nil
	}
	for _, name := range newAPIPolicyHeaderNames {
		req.Header.Del(name)
	}

	cfg, err := loadNewAPIPolicyConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled || c == nil || info == nil || info.ChannelMeta == nil {
		return nil
	}
	binding, found := matchNewAPIPolicyBinding(cfg.Bindings, req.URL, info.ApiKey)
	if !found {
		return nil
	}

	bodyDigest, err := requestBodySHA256(req, requestBody)
	if err != nil {
		return fmt.Errorf("hash upstream request body: %w", err)
	}
	if info.UserId <= 0 {
		return fmt.Errorf("signed Codex2API policy request requires a positive user id")
	}
	clientIP := strings.TrimSpace(c.ClientIP())
	if clientIP == "" {
		return fmt.Errorf("signed Codex2API policy request requires a client ip")
	}
	requestID := strings.TrimSpace(info.RequestId)
	if requestID == "" {
		requestID = strings.TrimSpace(c.GetString(common2.RequestIdKey))
	}
	if requestID == "" {
		requestID = common2.NewRequestId()
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	userID := strconv.Itoa(info.UserId)
	canonical := strings.Join([]string{
		"v1", timestamp, requestID, userID, clientIP, method, path, bodyDigest,
	}, "\n")

	meta := newAPIPolicyMeta{
		PlatformID:       binding.PlatformID,
		UserName:         common2.GetContextKeyString(c, constant.ContextKeyUserName),
		UserEmail:        info.UserEmail,
		UserGroup:        info.UserGroup,
		Profile:          binding.Profile,
		Mode:             binding.Mode,
		Provider:         "codex2api",
		Protocol:         string(info.GetFinalRequestRelayFormat()),
		OriginalEndpoint: originalNewAPIRequestPath(c),
		OriginalProtocol: string(info.RelayFormat),
		RequestedModel:   info.OriginModelName,
		UpstreamModel:    info.UpstreamModelName,
		ChannelID:        info.ChannelId,
	}
	metaJSON, err := common2.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal Codex2API policy metadata: %w", err)
	}
	encodedMeta := base64.RawURLEncoding.EncodeToString(metaJSON)
	metaCanonical := strings.Join([]string{newAPIPolicyMetaVersion, requestID, bodyDigest, encodedMeta}, "\n")

	req.Header.Set("X-NewAPI-User-ID", userID)
	req.Header.Set("X-NewAPI-Client-IP", clientIP)
	req.Header.Set("X-NewAPI-Request-ID", requestID)
	req.Header.Set("X-NewAPI-Timestamp", timestamp)
	req.Header.Set("X-NewAPI-Method", method)
	req.Header.Set("X-NewAPI-Path", path)
	req.Header.Set("X-NewAPI-Body-SHA256", bodyDigest)
	req.Header.Set("X-NewAPI-Signature-Version", newAPISignatureVersion)
	req.Header.Set("X-NewAPI-Signature", newAPIHMAC(binding.Secret, canonical))
	req.Header.Set("X-NewAPI-Policy-Meta", encodedMeta)
	req.Header.Set("X-NewAPI-Policy-Meta-Signature", newAPIHMAC(binding.Secret, metaCanonical))
	return nil
}

func loadNewAPIPolicyConfig() (newAPIPolicyConfig, error) {
	enabled, err := policyEnvBool("CODEX2API_POLICY_ENABLED", false)
	if err != nil || !enabled {
		return newAPIPolicyConfig{Enabled: enabled}, err
	}
	identityEnabled, err := policyEnvBool("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", true)
	if err != nil || !identityEnabled {
		return newAPIPolicyConfig{Enabled: false}, err
	}

	rawBindings := strings.TrimSpace(os.Getenv("CODEX2API_POLICY_BINDINGS"))
	if rawBindings != "" {
		var bindings []newAPIPolicyBinding
		if err := common2.Unmarshal([]byte(rawBindings), &bindings); err != nil {
			return newAPIPolicyConfig{}, fmt.Errorf("parse CODEX2API_POLICY_BINDINGS: %w", err)
		}
		for i := range bindings {
			if strings.TrimSpace(bindings[i].CodexKeyFingerprint) == "" {
				return newAPIPolicyConfig{}, fmt.Errorf("invalid CODEX2API_POLICY_BINDINGS[%d]: codex_key_fingerprint is required", i)
			}
			if err := normalizeNewAPIPolicyBinding(&bindings[i]); err != nil {
				return newAPIPolicyConfig{}, fmt.Errorf("invalid CODEX2API_POLICY_BINDINGS[%d]: %w", i, err)
			}
		}
		return newAPIPolicyConfig{Enabled: true, Bindings: bindings}, nil
	}

	secret := strings.TrimSpace(os.Getenv("CODEX2API_POLICY_SECRET"))
	targets, err := parseLegacyPolicyTargets(os.Getenv("CODEX2API_POLICY_TARGETS"))
	if err != nil {
		return newAPIPolicyConfig{}, err
	}
	if secret == "" && len(targets) == 0 {
		return newAPIPolicyConfig{Enabled: true}, nil
	}
	if len(secret) < 32 {
		return newAPIPolicyConfig{}, fmt.Errorf("CODEX2API_POLICY_SECRET must contain at least 32 characters")
	}
	platformID := strings.TrimSpace(os.Getenv("CODEX2API_POLICY_PLATFORM_ID"))
	if platformID == "" {
		platformID = "newapi"
	}
	bindings := make([]newAPIPolicyBinding, 0, len(targets))
	for _, target := range targets {
		binding := newAPIPolicyBinding{
			PlatformID: platformID,
			Target:     target,
			Secret:     secret,
			Enabled:    true,
		}
		if err := normalizeNewAPIPolicyBinding(&binding); err != nil {
			return newAPIPolicyConfig{}, fmt.Errorf("invalid CODEX2API_POLICY_TARGETS entry: %w", err)
		}
		bindings = append(bindings, binding)
	}
	return newAPIPolicyConfig{Enabled: true, Bindings: bindings}, nil
}

func policyEnvBool(name string, defaultValue bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}

func normalizeNewAPIPolicyBinding(binding *newAPIPolicyBinding) error {
	if binding == nil {
		return fmt.Errorf("binding is nil")
	}
	binding.PlatformID = strings.ToLower(strings.TrimSpace(binding.PlatformID))
	if !validNewAPIPlatformID(binding.PlatformID) {
		return fmt.Errorf("platform_id must match ^[a-z0-9][a-z0-9_-]{0,31}$")
	}
	binding.Target = strings.TrimSpace(binding.Target)
	if _, err := parsePolicyTarget(binding.Target); err != nil {
		return err
	}
	binding.CodexKeyFingerprint = strings.ToLower(strings.TrimSpace(binding.CodexKeyFingerprint))
	if binding.CodexKeyFingerprint != "" {
		decoded, err := hex.DecodeString(binding.CodexKeyFingerprint)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("codex_key_fingerprint must be a SHA-256 lowercase hexadecimal value")
		}
	}
	binding.Secret = strings.TrimSpace(binding.Secret)
	if len(binding.Secret) < 32 {
		return fmt.Errorf("secret must contain at least 32 characters")
	}
	binding.Profile = strings.ToLower(strings.TrimSpace(binding.Profile))
	if binding.Profile == "" {
		binding.Profile = "balanced"
	}
	switch binding.Profile {
	case "balanced", "strict", "research":
	default:
		return fmt.Errorf("profile must be balanced, strict, or research")
	}
	binding.Mode = strings.ToLower(strings.TrimSpace(binding.Mode))
	if binding.Mode == "" {
		binding.Mode = "shadow"
	}
	switch binding.Mode {
	case "off", "shadow", "warn", "enforce":
	default:
		return fmt.Errorf("mode must be off, shadow, warn, or enforce")
	}
	return nil
}

func validNewAPIPlatformID(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for i, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || i > 0 && (char == '_' || char == '-') {
			continue
		}
		return false
	}
	return true
}

func parseLegacyPolicyTargets(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var targets []string
		if err := common2.Unmarshal([]byte(raw), &targets); err != nil {
			return nil, fmt.Errorf("parse CODEX2API_POLICY_TARGETS: %w", err)
		}
		return targets, nil
	}
	return strings.FieldsFunc(raw, func(char rune) bool {
		return char == ',' || char == ';' || char == '\n' || char == '\r'
	}), nil
}

func matchNewAPIPolicyBinding(bindings []newAPIPolicyBinding, actual *url.URL, apiKey string) (newAPIPolicyBinding, bool) {
	keyDigest := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
	keyFingerprint := hex.EncodeToString(keyDigest[:])
	for _, binding := range bindings {
		if !binding.Enabled || !policyTargetMatches(binding.Target, actual) {
			continue
		}
		if binding.CodexKeyFingerprint != "" && !hmac.Equal([]byte(binding.CodexKeyFingerprint), []byte(keyFingerprint)) {
			continue
		}
		return binding, true
	}
	return newAPIPolicyBinding{}, false
}

func policyTargetMatches(rawTarget string, actual *url.URL) bool {
	target, err := parsePolicyTarget(rawTarget)
	if err != nil || actual == nil || !policySchemesMatch(target.Scheme, actual.Scheme) {
		return false
	}
	if !strings.EqualFold(target.Hostname(), actual.Hostname()) || policyURLPort(target) != policyURLPort(actual) {
		return false
	}
	targetPath := strings.TrimSuffix(target.EscapedPath(), "/")
	if targetPath == "" {
		return true
	}
	actualPath := strings.TrimSuffix(actual.EscapedPath(), "/")
	return actualPath == targetPath || strings.HasPrefix(actualPath, targetPath+"/")
}

func parsePolicyTarget(rawTarget string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(rawTarget))
	if err != nil {
		return nil, fmt.Errorf("parse target: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "ws" && target.Scheme != "wss" {
		return nil, fmt.Errorf("target must use http, https, ws, or wss")
	}
	if target.Hostname() == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, fmt.Errorf("target must be an absolute URL without credentials, query, or fragment")
	}
	return target, nil
}

func policySchemesMatch(left string, right string) bool {
	normalize := func(value string) string {
		switch strings.ToLower(value) {
		case "ws":
			return "http"
		case "wss":
			return "https"
		default:
			return strings.ToLower(value)
		}
	}
	return normalize(left) == normalize(right)
}

func policyURLPort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	switch strings.ToLower(value.Scheme) {
	case "http", "ws":
		return "80"
	case "https", "wss":
		return "443"
	default:
		return ""
	}
}

func requestBodySHA256(req *http.Request, requestBody io.Reader) (string, error) {
	hash := sha256.New()
	if req != nil && req.GetBody != nil {
		copyBody, err := req.GetBody()
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hash, copyBody)
		closeErr := copyBody.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	if requestBody == nil {
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	if source, ok := requestBody.(interface{ SourceReader() io.Reader }); ok {
		requestBody = source.SourceReader()
	}
	seeker, ok := requestBody.(io.ReadSeeker)
	if !ok {
		return "", fmt.Errorf("outbound body is not rewindable")
	}
	position, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := io.Copy(hash, seeker); err != nil {
		_, _ = seeker.Seek(position, io.SeekStart)
		return "", err
	}
	if _, err := seeker.Seek(position, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func originalNewAPIRequestPath(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	path := c.Request.URL.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

func newAPIHMAC(secret string, canonical string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}
