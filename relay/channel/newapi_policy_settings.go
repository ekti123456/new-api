package channel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// This single option is deliberately secret-suffixed and excluded from the
// generic options API. Only the dedicated redacted editor may read it.
const NewAPIPolicyOption = "Codex2APIPolicySecret"

type NewAPIPolicySettings struct {
	Enabled                bool                  `json:"enabled"`
	IdentityForwardEnabled bool                  `json:"identity_forward_enabled"`
	AuditEnabled           bool                  `json:"audit_enabled"`
	StrikeEnabled          bool                  `json:"strike_enabled"`
	AccountBanEnabled      bool                  `json:"account_ban_enabled"`
	IPBlockEnabled         bool                  `json:"ip_block_enabled"`
	BanAfter               int                   `json:"ban_after"`
	WindowSeconds          int                   `json:"window_seconds"`
	CPAInstanceID          string                `json:"cpa_instance_id"`
	Bindings               []newAPIPolicyBinding `json:"bindings"`
}

// Binding fields match the existing signed policy contract.
type NewAPIPolicyBinding = newAPIPolicyBinding

func (s NewAPIPolicySettings) runtime() newAPIPolicyConfig {
	return newAPIPolicyConfig{Enabled: s.Enabled && s.IdentityForwardEnabled, Bindings: s.Bindings,
		Enforcement: newAPIPolicyEnforcementConfig{AuditEnabled: s.AuditEnabled, StrikeEnabled: s.StrikeEnabled,
			AccountBanEnabled: s.AccountBanEnabled, IPBlockEnabled: s.IPBlockEnabled, BanAfter: s.BanAfter, WindowSeconds: s.WindowSeconds}}
}

func savedNewAPIPolicySettings() (NewAPIPolicySettings, bool, error) {
	common.OptionMapRWMutex.RLock()
	raw, ok := common.OptionMap[NewAPIPolicyOption]
	common.OptionMapRWMutex.RUnlock()
	if !ok {
		return NewAPIPolicySettings{}, false, nil
	}
	var s NewAPIPolicySettings
	if common.UnmarshalJsonStr(common.Interface2String(raw), &s) != nil {
		return s, true, fmt.Errorf("saved Codex2API policy configuration is invalid")
	}
	if err := s.Normalize(); err != nil {
		return s, true, err
	}
	return s, true, nil
}

func (s *NewAPIPolicySettings) Normalize() error {
	if s.BanAfter < 1 || s.BanAfter > 1000 || s.WindowSeconds < 60 || s.WindowSeconds > 31536000 {
		return fmt.Errorf("ban_after must be 1-1000; window_seconds must be 60-31536000")
	}
	if (s.AccountBanEnabled || s.IPBlockEnabled) && !s.StrikeEnabled {
		return fmt.Errorf("enable strike accumulation before account or IP penalties")
	}
	s.CPAInstanceID = strings.TrimSpace(s.CPAInstanceID)
	if len(s.CPAInstanceID) > 256 || strings.ContainsAny(s.CPAInstanceID, "\r\n\x00") {
		return fmt.Errorf("invalid CPA instance ID")
	}
	if len(s.Bindings) > 32 {
		return fmt.Errorf("at most 32 audit connections are supported")
	}
	seen := map[string]bool{}
	for i := range s.Bindings {
		b := &s.Bindings[i]
		if err := normalizeNewAPIPolicyBinding(b); err != nil {
			return fmt.Errorf("connection %d: %w", i+1, err)
		}
		if len(b.Target) > 2048 || len(b.Secret) > 4096 {
			return fmt.Errorf("connection %d is too long", i+1)
		}
		key := strings.ToLower(strings.TrimRight(b.Target, "/")) + "\x00" + b.CodexKeyFingerprint
		if seen[key] {
			return fmt.Errorf("duplicate target and API key")
		}
		seen[key] = true
	}
	if s.Bindings == nil {
		s.Bindings = []newAPIPolicyBinding{}
	}
	return nil
}

// Settings in the DB override all legacy policy environment variables as one
// snapshot. Before the first save, old deployments keep their existing values.
func ReadNewAPIPolicySettings() (s NewAPIPolicySettings, source, revision string, err error) {
	var saved bool
	s, saved, err = savedNewAPIPolicySettings()
	source = "database"
	if err != nil {
		return
	}
	if !saved {
		source = "environment"
		s.IdentityForwardEnabled, err = policyEnvBool("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", true)
		if err != nil {
			return
		}
		s.Enabled, err = policyEnvBool("CODEX2API_POLICY_ENABLED", false)
		if err != nil {
			return
		}
		var e newAPIPolicyEnforcementConfig
		e, err = loadNewAPIPolicyEnforcementConfig()
		if err != nil {
			return
		}
		s.AuditEnabled, s.StrikeEnabled, s.AccountBanEnabled, s.IPBlockEnabled = e.AuditEnabled, e.StrikeEnabled, e.AccountBanEnabled, e.IPBlockEnabled
		s.BanAfter, s.WindowSeconds = e.BanAfter, e.WindowSeconds
		s.CPAInstanceID = strings.TrimSpace(os.Getenv("CPA_IDENTITY_INSTANCE_ID"))
		raw := strings.TrimSpace(os.Getenv("CODEX2API_POLICY_BINDINGS"))
		if raw != "" {
			if common.UnmarshalJsonStr(raw, &s.Bindings) != nil {
				err = fmt.Errorf("invalid CODEX2API_POLICY_BINDINGS")
				return
			}
		} else {
			var targets []string
			targets, err = parseLegacyPolicyTargets(os.Getenv("CODEX2API_POLICY_TARGETS"))
			if err != nil {
				return
			}
			platform := strings.TrimSpace(os.Getenv("CODEX2API_POLICY_PLATFORM_ID"))
			if platform == "" {
				platform = "newapi"
			}
			for _, target := range targets {
				s.Bindings = append(s.Bindings, newAPIPolicyBinding{Target: target, PlatformID: platform, Secret: strings.TrimSpace(os.Getenv("CODEX2API_POLICY_SECRET")), Enabled: true})
			}
		}
		err = s.Normalize()
		if err != nil {
			return
		}
	}
	raw, marshalErr := common.Marshal(s)
	if marshalErr != nil {
		err = marshalErr
		return
	}
	digest := sha256.Sum256(append([]byte(source+"\x00"), raw...))
	revision = hex.EncodeToString(digest[:])
	return
}

func NewAPIPolicyBindingID(b NewAPIPolicyBinding) string {
	digest := sha256.Sum256([]byte(b.PlatformID + "\x00" + b.Target + "\x00" + b.CodexKeyFingerprint))
	return hex.EncodeToString(digest[:16])
}

func CPAIdentityInstanceID() string {
	s, saved, err := savedNewAPIPolicySettings()
	if err != nil {
		return ""
	}
	if saved {
		return s.CPAInstanceID
	}
	return strings.TrimSpace(os.Getenv("CPA_IDENTITY_INSTANCE_ID"))
}
