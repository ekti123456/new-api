package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

type codexConnectionEditor struct {
	channel.NewAPIPolicyBinding
	ID               string `json:"id"`
	APIKey           string `json:"api_key"`
	SecretConfigured bool   `json:"secret_configured"`
}

type codexPolicyEditor struct {
	Settings     channel.NewAPIPolicySettings `json:"settings"`
	Connections  []codexConnectionEditor      `json:"connections"`
	Revision     string                       `json:"revision"`
	Source       string                       `json:"source,omitempty"`
	CPASupported bool                         `json:"cpa_supported"`
}

const cpaIdentitySettingsSupported = true

var codexPolicyEditorMu sync.Mutex

func redactedCodexPolicyEditor(s channel.NewAPIPolicySettings, source, revision string) codexPolicyEditor {
	e := codexPolicyEditor{Settings: s, Source: source, Revision: revision, Connections: []codexConnectionEditor{}, CPASupported: cpaIdentitySettingsSupported}
	for _, b := range s.Bindings {
		entry := codexConnectionEditor{NewAPIPolicyBinding: b, ID: channel.NewAPIPolicyBindingID(b), SecretConfigured: b.Secret != ""}
		entry.Secret = ""
		e.Connections = append(e.Connections, entry)
	}
	e.Settings.Bindings = nil
	return e
}

func GetCodex2APIPolicySettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	s, source, revision, err := channel.ReadNewAPIPolicySettings()
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(200, gin.H{"success": true, "data": redactedCodexPolicyEditor(s, source, revision)})
}

func resolveCodexConnection(entry codexConnectionEditor, old channel.NewAPIPolicySettings) (channel.NewAPIPolicyBinding, error) {
	b := entry.NewAPIPolicyBinding
	var previous *channel.NewAPIPolicyBinding
	for i := range old.Bindings {
		if channel.NewAPIPolicyBindingID(old.Bindings[i]) == entry.ID {
			previous = &old.Bindings[i]
			break
		}
	}
	if entry.ID != "" && previous == nil {
		return b, fmt.Errorf("connection changed; reload settings")
	}
	if previous != nil {
		if b.Secret == "" {
			if b.Target != previous.Target || b.PlatformID != previous.PlatformID {
				return b, fmt.Errorf("re-enter audit secret when changing target or platform")
			}
			b.Secret = previous.Secret
		}
		b.CodexKeyFingerprint = previous.CodexKeyFingerprint
	} else {
		b.CodexKeyFingerprint = ""
	}
	if key := strings.TrimSpace(entry.APIKey); key != "" {
		if len(key) > 4096 || strings.ContainsAny(key, "\r\n") {
			return b, fmt.Errorf("invalid API key")
		}
		digest := sha256.Sum256([]byte(key))
		b.CodexKeyFingerprint = hex.EncodeToString(digest[:])
	}
	if previous == nil && b.CodexKeyFingerprint == "" {
		return b, fmt.Errorf("API key is required for a new connection")
	}
	u, err := url.Parse(b.Target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return b, fmt.Errorf("connection URL must use HTTP or HTTPS")
	}
	test := old
	test.Bindings = []channel.NewAPIPolicyBinding{b}
	if err := test.Normalize(); err != nil {
		return b, err
	}
	return test.Bindings[0], nil
}

func SaveCodex2APIPolicySettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input codexPolicyEditor
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10), &input) != nil {
		common.ApiErrorMsg(c, "Invalid audit settings")
		return
	}
	codexPolicyEditorMu.Lock()
	defer codexPolicyEditorMu.Unlock()
	old, _, revision, err := channel.ReadNewAPIPolicySettings()
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if input.Revision != revision {
		c.JSON(409, gin.H{"success": false, "message": "Settings changed; reload before saving"})
		return
	}
	if len(input.Connections) > 32 {
		common.ApiErrorMsg(c, "At most 32 audit connections are supported")
		return
	}
	s := input.Settings
	s.Bindings = nil
	for _, entry := range input.Connections {
		b, err := resolveCodexConnection(entry, old)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		s.Bindings = append(s.Bindings, b)
	}
	if err = s.Normalize(); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	raw, err := common.Marshal(s)
	if err != nil {
		common.ApiErrorMsg(c, "Cannot encode audit settings")
		return
	}
	if err = model.UpdateOptionsBulk(map[string]string{channel.NewAPIPolicyOption: string(raw)}); err != nil {
		common.ApiErrorMsg(c, "Cannot save audit settings")
		return
	}
	GetCodex2APIPolicySettings(c)
}

// The actual calling key stays in channel management. The audit editor stores
// only its fingerprint, and resolves it from a matching channel for checks.
func codexPolicyChannelKey(ctx context.Context, b channel.NewAPIPolicyBinding) string {
	rows, err := model.ProjectIntegrationChannels(ctx)
	if err != nil {
		return ""
	}
	scope := channel.ProjectSummaryScope{Targets: []string{b.Target}, Fingerprint: b.CodexKeyFingerprint}
	for _, row := range rows {
		if row.Status != common.ChannelStatusEnabled {
			continue
		}
		for _, key := range row.GetKeys() {
			if scope.Matches(row.GetBaseURL(), []string{key}) {
				return key
			}
		}
	}
	return ""
}

func TestCodex2APIConnection(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var entry codexConnectionEditor
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 16384), &entry) != nil {
		common.ApiErrorMsg(c, "Invalid connection")
		return
	}
	old, _, _, err := channel.ReadNewAPIPolicySettings()
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	b, err := resolveCodexConnection(entry, old)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	key := strings.TrimSpace(entry.APIKey)
	if key == "" {
		key = codexPolicyChannelKey(ctx, b)
	}
	result := channel.ProbeNewAPIPolicy(ctx, b, key)
	c.JSON(200, gin.H{"success": true, "data": result})
}

var codexPolicyProbes = struct {
	sync.Mutex
	Results map[string]channel.NewAPIPolicyProbeResult
}{Results: map[string]channel.NewAPIPolicyProbeResult{}}
var codexPolicyProbeFlight singleflight.Group

func GetCodex2APIConnectionStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	s, _, revision, err := channel.ReadNewAPIPolicySettings()
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	results := map[string]channel.NewAPIPolicyProbeResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, binding := range s.Bindings {
		b := binding
		id := channel.NewAPIPolicyBindingID(b)
		if !s.Enabled || !s.IdentityForwardEnabled || !b.Enabled {
			mu.Lock()
			results[id] = channel.NewAPIPolicyProbeResult{State: "disabled"}
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			cacheKey := revision + ":" + id
			value, _, _ := codexPolicyProbeFlight.Do(cacheKey, func() (any, error) {
				codexPolicyProbes.Lock()
				previous, ok := codexPolicyProbes.Results[cacheKey]
				codexPolicyProbes.Unlock()
				if ok && time.Since(previous.CheckedAt) < 25*time.Second {
					return previous, nil
				}
				result := channel.ProbeNewAPIPolicy(ctx, b, codexPolicyChannelKey(ctx, b))
				codexPolicyProbes.Lock()
				if len(codexPolicyProbes.Results) > 128 {
					clear(codexPolicyProbes.Results)
				}
				codexPolicyProbes.Results[cacheKey] = result
				codexPolicyProbes.Unlock()
				return result, nil
			})
			if result, ok := value.(channel.NewAPIPolicyProbeResult); ok {
				mu.Lock()
				results[id] = result
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	c.JSON(200, gin.H{"success": true, "data": results, "revision": revision})
}
