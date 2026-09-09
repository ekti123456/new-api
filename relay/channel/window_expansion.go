package channel

import (
	"bytes"
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type WindowControlInput struct {
	Operation      string  `json:"operation"`
	AllowExpansion bool    `json:"allow_expansion"`
	ExtraLimit     int     `json:"extra_limit"`
	Multiplier     float64 `json:"multiplier"`
	GrantID        string  `json:"grant_id,omitempty"`
	ReservationID  string  `json:"reservation_id,omitempty"`
}

type WindowAdmissionDenied struct{ Message string }

func (denied *WindowAdmissionDenied) Error() string {
	if denied.Message != "" {
		return denied.Message
	}
	return "当前无法创建窗口，请复用已有窗口或等待恢复"
}

type PersonalWindow struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Model      string    `json:"model,omitempty"`
	Expanded   bool      `json:"expanded"`
	Multiplier float64   `json:"multiplier"`
}

type WindowControlResult struct {
	Truncated           bool             `json:"truncated"`
	Reason              string           `json:"reason,omitempty"`
	Version             int              `json:"version"`
	Ticket              string           `json:"ticket"`
	ServerNow           time.Time        `json:"server_now"`
	Limit               int              `json:"limit"`
	Used                int              `json:"used"`
	WindowSeconds       int              `json:"window_seconds"`
	CreationAvailableAt *time.Time       `json:"creation_available_at"`
	CooldownUnavailable bool             `json:"cooldown_unavailable"`
	Windows             []PersonalWindow `json:"windows"`
}

func windowControlDestination(info *relaycommon.RelayInfo) (*url.URL, newAPIPolicyBinding, error) {
	if info == nil || info.ChannelMeta == nil {
		return nil, newAPIPolicyBinding{}, errors.New("missing window destination")
	}
	target, err := url.Parse(strings.TrimRight(info.ChannelBaseUrl, "/"))
	if err != nil || target == nil || target.Host == "" {
		return nil, newAPIPolicyBinding{}, errors.New("invalid window destination")
	}
	target.Path = strings.TrimSuffix(strings.TrimRight(target.Path, "/"), "/v1") + "/v1/session-windows"
	target.RawPath, target.RawQuery, target.Fragment = "", "", ""
	config, err := loadNewAPIPolicyConfig()
	if err != nil || !config.Enabled {
		return nil, newAPIPolicyBinding{}, errors.New("signed Codex2API integration is unavailable")
	}
	binding, matched := matchNewAPIPolicyBinding(config.Bindings, target, info.ApiKey)
	if !matched {
		return nil, newAPIPolicyBinding{}, errors.New("window destination requires a verified Codex2API binding")
	}
	return target, binding, nil
}

func windowBindingHash(binding newAPIPolicyBinding, key string) string {
	digest := sha256.Sum256([]byte(binding.PlatformID + "\n" + binding.Target + "\n" + binding.Secret + "\n" + key))
	return hex.EncodeToString(digest[:])
}

func WindowServiceIdentity(info *relaycommon.RelayInfo) (string, error) {
	target, binding, err := windowControlDestination(info)
	if err != nil {
		return "", err
	}
	return binding.PlatformID + "\n" + target.String(), nil
}

func RequestUserWindows(requestContext *gin.Context, info *relaycommon.RelayInfo, input WindowControlInput) (WindowControlResult, error) {
	var result WindowControlResult
	controlInfo := *info
	controlInfo.RequestId = common.NewRequestId()
	controlInfo.WindowBilling = nil
	info = &controlInfo
	target, _, err := windowControlDestination(info)
	if err != nil {
		return result, err
	}
	payload, err := common.Marshal(input)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(requestContext.Request.Context(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+info.ApiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "NewAPI-Window-Control/1")
	if err = applyNewAPIPolicyHeaders(requestContext, request, info, bytes.NewReader(payload)); err != nil {
		return result, err
	}
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return result, errors.New("window transport is unavailable")
	}
	boundedClient := *client
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := boundedClient.Do(request)
	if err != nil {
		return result, errors.New("窗口服务暂时无法连接，请稍后重试")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 512*1024+1))
	if err != nil || len(body) > 512*1024 {
		return result, errors.New("invalid window response")
	}
	if response.StatusCode == http.StatusBadRequest {
		var detail struct {
			Message string `json:"message"`
		}
		if common.Unmarshal(body, &detail) != nil || len(detail.Message) > 1000 {
			detail.Message = ""
		}
		return result, &WindowAdmissionDenied{Message: detail.Message}
	}
	if response.StatusCode != http.StatusOK {
		return result, errors.New("窗口服务暂时不可用，请稍后重试")
	}
	if common.Unmarshal(body, &result) != nil || result.Version != 1 {
		return result, errors.New("Codex2API must be upgraded to support window management")
	}
	return result, nil
}

func verifyWindowBillingTicket(binding newAPIPolicyBinding, key string, userID int, fingerprint, ticket string) (*relaycommon.WindowBillingGrant, error) {
	payload, signature, found := strings.Cut(ticket, ".")
	decodedSignature, err := hex.DecodeString(signature)
	mac := hmac.New(sha256.New, []byte(binding.Secret))
	mac.Write([]byte("codex2api-window-grant-v1\n" + payload))
	if !found || len(ticket) > 4096 || err != nil || !hmac.Equal(decodedSignature, mac.Sum(nil)) {
		return nil, errors.New("invalid window billing signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	var envelope struct {
		Version       int                            `json:"version"`
		Platform      string                         `json:"platform"`
		UserID        string                         `json:"user_id"`
		Fingerprint   string                         `json:"root_fingerprint"`
		Grant         relaycommon.WindowBillingGrant `json:"grant"`
		ReservationID string                         `json:"reservation_id,omitempty"`
	}
	if err != nil || common.Unmarshal(raw, &envelope) != nil {
		return nil, errors.New("invalid billing grant")
	}
	grant := envelope.Grant
	if envelope.Version != 1 || envelope.Platform != binding.PlatformID || envelope.UserID != strconv.Itoa(userID) || envelope.Fingerprint != fingerprint || grant.ID == "" || len(envelope.ReservationID) > 64 || (grant.NoWindow && grant.Expanded) || !grant.ExpiresAt.After(time.Now()) || grant.Multiplier < 1 || grant.Multiplier > 10 || math.IsNaN(grant.Multiplier) || math.IsInf(grant.Multiplier, 0) || (!grant.Expanded && grant.Multiplier != 1) {
		return nil, errors.New("window billing scope or tariff mismatch")
	}
	grant.Ticket, grant.BindingHash, grant.Fingerprint = ticket, windowBindingHash(binding, key), fingerprint
	grant.ReservationID = envelope.ReservationID
	return &grant, nil
}

type cachedWindowGrant struct {
	grant   relaycommon.WindowBillingGrant
	until   time.Time
	element *list.Element
}

type windowGrantCache struct {
	sync.Mutex
	items   map[string]cachedWindowGrant
	order   *list.List
	maximum int
}

func newWindowGrantCache(maximum int) *windowGrantCache {
	return &windowGrantCache{items: make(map[string]cachedWindowGrant), order: list.New(), maximum: maximum}
}

func (cache *windowGrantCache) getLocked(key string) (cachedWindowGrant, bool) {
	value, found := cache.items[key]
	if found && value.element != nil {
		cache.order.MoveToFront(value.element)
	}
	return value, found
}

func (cache *windowGrantCache) deleteLocked(key string) {
	if value, found := cache.items[key]; found && value.element != nil {
		cache.order.Remove(value.element)
	}
	delete(cache.items, key)
}

func (cache *windowGrantCache) putLocked(key string, value cachedWindowGrant) {
	cache.deleteLocked(key)
	if len(cache.items) >= cache.maximum {
		if oldest := cache.order.Back(); oldest != nil {
			cache.deleteLocked(oldest.Value.(string))
		}
	}
	value.element = cache.order.PushFront(key)
	cache.items[key] = value
}

var windowBillingCache = newWindowGrantCache(8192)

func PrepareWindowBilling(requestContext *gin.Context, info *relaycommon.RelayInfo) (func(bool), error) {
	if info == nil || requestContext == nil || requestContext.Request == nil || requestContext.Request.URL == nil || requestContext.Request.Method != http.MethodPost {
		return nil, nil
	}
	switch requestContext.Request.URL.Path {
	case "/v1/responses", "/v1/responses/compact", "/v1/chat/completions", "/v1/messages":
	default:
		return nil, nil
	}
	policy := operation_setting.GetWindowExpansionPolicy()
	if !policy.Enabled && !info.UserSetting.WindowExpansionJoined && !requestContext.GetBool("window_billing_checked") {
		return nil, nil
	}
	shadow := *info
	shadow.InitChannelMeta(requestContext)
	configured := slices.Contains(policy.ChannelIDs, shadow.ChannelId)
	if !configured && !info.UserSetting.WindowExpansionJoined && !requestContext.GetBool("window_billing_checked") {
		return nil, nil
	}
	_, binding, err := windowControlDestination(&shadow)
	if err != nil {
		if configured {
			return nil, err
		}
		return nil, nil
	}
	root := applyCodexPassiveRootSessionOverride(requestContext, analyzeNewAPIPolicyRootSession(requestContext, &shadow, newAPIPolicyStableSessionID(requestContext, &shadow)))
	if root.state != newAPIPolicyRootSessionResolved {
		return nil, nil
	}
	fingerprint := newAPIPolicyRootSessionFingerprint(binding.PlatformID, strconv.Itoa(info.UserId), root.rootID)
	cacheKey := windowBindingHash(binding, shadow.ApiKey) + ":" + strconv.Itoa(info.UserId) + ":" + fingerprint
	if expected := requestContext.GetString("window_billing_scope"); expected != "" && expected != cacheKey {
		return nil, errors.New("window renewal cannot change its root or destination")
	}
	requestContext.Set("window_billing_checked", true)
	requestContext.Set("window_billing_scope", cacheKey)
	windowBillingCache.Lock()
	cached, found := windowBillingCache.getLocked(cacheKey)
	windowBillingCache.Unlock()
	if found && cached.until.After(time.Now()) && (cached.grant.ID == "" || (cached.grant.Confirmed && cached.grant.ExpiresAt.After(time.Now()))) {
		if cached.grant.ID == "" {
			return nil, nil
		}
		copy := cached.grant
		info.WindowBilling = &copy
		requestContext.Set("window_billing_pinned", true)
		return func(success bool) { finishWindowAuthorization(requestContext, info, &shadow, cacheKey, success) }, nil
	}
	preferences, err := model.GetUserSetting(info.UserId, true)
	if err != nil {
		return nil, errors.New("窗口扩容设置暂时无法确认，请稍后重试")
	}
	allow := configured && policy.Enabled && preferences.WindowExpansionEnabled && preferences.WindowExpansionAcceptedRatio >= policy.Multiplier
	result, err := RequestUserWindows(requestContext, &shadow, WindowControlInput{Operation: "quote", AllowExpansion: allow, ExtraLimit: policy.ExtraLimit, Multiplier: policy.Multiplier, ReservationID: common.GetUUID()})
	if err != nil {
		return nil, err
	}
	if result.Ticket == "" {
		if result.Reason == "ordinary_only" {
			windowBillingCache.Lock()
			windowBillingCache.putLocked(cacheKey, cachedWindowGrant{until: time.Now().Add(5 * time.Second)})
			windowBillingCache.Unlock()
		}
		return nil, nil
	}
	grant, err := verifyWindowBillingTicket(binding, shadow.ApiKey, info.UserId, fingerprint, result.Ticket)
	if err != nil {
		return nil, err
	}
	info.WindowBilling = grant
	requestContext.Set("window_billing_pinned", true)
	return func(success bool) { finishWindowAuthorization(requestContext, info, &shadow, cacheKey, success) }, nil
}

func finishWindowAuthorization(request *gin.Context, info, destination *relaycommon.RelayInfo, cacheKey string, success bool) {
	grant := info.WindowBilling
	if grant == nil {
		return
	}
	windowBillingCache.Lock()
	if grant.Confirmed && success && grant.ExpiresAt.After(time.Now()) {
		windowBillingCache.putLocked(cacheKey, cachedWindowGrant{grant: *grant, until: grant.ExpiresAt})
	} else if current := windowBillingCache.items[cacheKey]; current.grant.ID == grant.ID && (!current.grant.Confirmed || grant.Confirmed) {
		windowBillingCache.deleteLocked(cacheKey)
	}
	windowBillingCache.Unlock()
	if grant.Confirmed || grant.ReservationID == "" {
		return
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(request.Request.Context()), time.Second)
	defer cancel()
	cleanup := request.Copy()
	cleanup.Request = request.Request.Clone(cleanupContext)
	_, _ = RequestUserWindows(cleanup, destination, WindowControlInput{Operation: "release", GrantID: grant.ID, ReservationID: grant.ReservationID, Multiplier: 1})
}

func acceptWindowAuthorizationResponse(request *gin.Context, response *http.Response, info *relaycommon.RelayInfo) error {
	ticket := response.Header.Get("X-Codex2API-Window-Grant")
	response.Header.Del("X-Codex2API-Window-Grant")
	if ticket == "" || info.WindowBilling == nil {
		return nil
	}
	_, binding, err := windowControlDestination(info)
	if err != nil {
		return err
	}
	previous := info.WindowBilling
	grant, err := verifyWindowBillingTicket(binding, info.ApiKey, info.UserId, previous.Fingerprint, ticket)
	if err != nil {
		return err
	}
	if !grant.Confirmed || grant.BindingHash != previous.BindingHash || grant.ID != previous.ID || grant.Root != previous.Root || grant.Expanded != previous.Expanded || grant.Multiplier != previous.Multiplier || !grant.ExpiresAt.Equal(previous.ExpiresAt) {
		return errors.New("window confirmation does not match the authorized tariff")
	}
	info.WindowBilling = grant
	return nil
}

func RefreshWindowBilling(request *gin.Context, info *relaycommon.RelayInfo) (func(bool), error) {
	if info == nil || !request.GetBool("window_billing_checked") {
		return nil, errors.New("window authorization is missing")
	}
	previous := info.WindowBilling
	cacheKey := request.GetString("window_billing_scope")
	destination := *info
	destination.InitChannelMeta(request)
	finishWindowAuthorization(request, info, &destination, cacheKey, false)
	windowBillingCache.Lock()
	if current := windowBillingCache.items[cacheKey]; previous == nil || current.grant.ID == previous.ID {
		windowBillingCache.deleteLocked(cacheKey)
	}
	windowBillingCache.Unlock()
	info.WindowBilling = nil
	finish, err := PrepareWindowBilling(request, info)
	if err != nil {
		info.WindowBilling = previous
		return nil, err
	}
	if previous != nil && info.WindowBilling != nil && (info.WindowBilling.BindingHash != previous.BindingHash || info.WindowBilling.Fingerprint != previous.Fingerprint) {
		info.WindowBilling = previous
		return nil, errors.New("window renewal cannot change its root or destination")
	}
	return finish, nil
}

func ValidateWindowBillingDestination(info *relaycommon.RelayInfo, binding newAPIPolicyBinding, fingerprint string) error {
	if info.WindowBilling == nil {
		return nil
	}
	if info.WindowBilling.BindingHash != windowBindingHash(binding, info.ApiKey) || info.WindowBilling.Fingerprint != fingerprint {
		return fmt.Errorf("window billing route changed; retry the original root window")
	}
	return nil
}
