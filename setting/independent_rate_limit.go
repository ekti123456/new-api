package setting

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const IndependentRateLimitOption = "IndependentRequestRateLimit"

type UserRateOverride struct {
	UserID int `json:"user_id"`
	Limit  int `json:"limit"`
}

type IndependentRateRule struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Enabled   bool               `json:"enabled"`
	Path      string             `json:"path"`
	UAMode    string             `json:"ua_mode"`
	UA        string             `json:"ua"`
	Stream    string             `json:"stream"`
	Limit     int                `json:"limit"`
	Overrides []UserRateOverride `json:"overrides"`
}

type IndependentRateLimits struct {
	Enabled   bool                  `json:"enabled"`
	Namespace string                `json:"namespace"`
	Rules     []IndependentRateRule `json:"rules"`
}

var rateRuleID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var independentRateCache struct {
	sync.Mutex
	raw      string
	revision string
	config   *IndependentRateLimits
	err      error
}

func (s *IndependentRateLimits) Validate() error {
	if len(s.Rules) > 64 {
		return fmt.Errorf("at most 64 independent rate rules are supported")
	}
	if s.Namespace != "" && !rateRuleID.MatchString(s.Namespace) {
		return fmt.Errorf("invalid rate limit namespace")
	}
	if s.Enabled && s.Namespace == "" {
		return fmt.Errorf("rate limit namespace is required")
	}
	seen := map[string]bool{}
	if s.Rules == nil {
		s.Rules = []IndependentRateRule{}
	}
	for i := range s.Rules {
		rule := &s.Rules[i]
		if rule.Overrides == nil {
			rule.Overrides = []UserRateOverride{}
		}
		if !rateRuleID.MatchString(rule.ID) || seen[rule.ID] {
			return fmt.Errorf("rule IDs must be valid and unique")
		}
		seen[rule.ID] = true
		if strings.TrimSpace(rule.Name) == "" || utf8.RuneCountInString(rule.Name) > 100 {
			return fmt.Errorf("rule name must contain 1-100 characters")
		}
		if !strings.HasPrefix(rule.Path, "/") || len(rule.Path) > 256 || strings.ContainsAny(rule.Path, "?#*\r\n\t ") {
			return fmt.Errorf("rule path must be an exact absolute request path")
		}
		if rule.UAMode != "any" && rule.UAMode != "exact" && rule.UAMode != "contains" {
			return fmt.Errorf("invalid UA matching mode")
		}
		if len(rule.UA) > 512 || (rule.UAMode != "any" && rule.UA == "") || strings.ContainsAny(rule.UA, "\r\n") {
			return fmt.Errorf("invalid UA matcher")
		}
		if rule.Stream != "any" && rule.Stream != "stream" && rule.Stream != "non_stream" {
			return fmt.Errorf("invalid stream matcher")
		}
		if rule.Limit < 1 || rule.Limit > 100000 {
			return fmt.Errorf("RPM must be between 1 and 100000")
		}
		if len(rule.Overrides) > 2000 {
			return fmt.Errorf("at most 2000 user overrides per rule")
		}
		users := map[int]bool{}
		for _, override := range rule.Overrides {
			if override.UserID <= 0 || users[override.UserID] || override.Limit < 1 || override.Limit > 100000 {
				return fmt.Errorf("user overrides require unique positive user IDs and RPM between 1 and 100000")
			}
			users[override.UserID] = true
		}
	}
	return nil
}

func (r IndependentRateRule) MatchesRequest(path, ua string) bool {
	if !r.Enabled || r.Path != path {
		return false
	}
	switch r.UAMode {
	case "exact":
		return ua == r.UA
	case "contains":
		return strings.Contains(ua, r.UA)
	default:
		return r.UAMode == "any"
	}
}

func (r IndependentRateRule) UserLimit(userID int) int {
	for _, override := range r.Overrides {
		if override.UserID == userID {
			return override.Limit
		}
	}
	return r.Limit
}

// Returned snapshots are immutable. Only changed option bytes are parsed, so
// ordinary requests do not repeatedly decode a potentially large rule list.
func ReadIndependentRateLimits() (*IndependentRateLimits, string, error) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[IndependentRateLimitOption]
	common.OptionMapRWMutex.RUnlock()
	independentRateCache.Lock()
	defer independentRateCache.Unlock()
	if independentRateCache.config == nil || independentRateCache.raw != raw {
		s := &IndependentRateLimits{Rules: []IndependentRateRule{}}
		var err error
		if raw != "" {
			err = common.UnmarshalJsonStr(raw, s)
		}
		if err == nil {
			err = s.Validate()
		}
		independentRateCache.raw, independentRateCache.config, independentRateCache.err = raw, s, err
		independentRateCache.revision = fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	}
	return independentRateCache.config, independentRateCache.revision, independentRateCache.err
}
