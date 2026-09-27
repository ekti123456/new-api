package channel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProjectSummaryScope contains no secrets. Only exact key-scoped bindings can
// authorize this purpose-separated, read-only endpoint.
type ProjectSummaryScope struct {
	Targets     []string
	Fingerprint string
	DayStart    int64
}

var projectSummaryNonces = struct {
	sync.Mutex
	expires map[string]int64
}{expires: map[string]int64{}}

func VerifyProjectSummary(r *http.Request, now time.Time) (*ProjectSummaryScope, error) {
	denied := errors.New("project integration authentication failed")
	if r.Method != http.MethodGet {
		return nil, denied
	}
	names := []string{"Timestamp", "Nonce", "Platform", "Key-Fingerprint", "Day-Start"}
	fields := make([]string, len(names))
	for i, name := range names {
		fields[i] = r.Header.Get("X-CPA-" + name)
		if len(fields[i]) == 0 || len(fields[i]) > 128 || strings.ContainsAny(fields[i], "\r\n") {
			return nil, denied
		}
	}
	ts, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || ts < now.Unix()-60 || ts > now.Unix()+60 {
		return nil, denied
	}
	day, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil || day <= 0 || day > now.Unix() || now.Unix()-day > 27*3600 {
		return nil, denied
	}
	nonce, err := hex.DecodeString(fields[1])
	if err != nil || len(nonce) != 16 {
		return nil, denied
	}
	fp, err := hex.DecodeString(fields[3])
	if err != nil || len(fp) != sha256.Size {
		return nil, denied
	}
	sig, err := hex.DecodeString(r.Header.Get("X-CPA-Signature"))
	if err != nil || len(sig) != sha256.Size {
		return nil, denied
	}
	cfg, err := loadNewAPIPolicyConfig()
	if err != nil || !cfg.Enabled {
		return nil, denied
	}
	scope := &ProjectSummaryScope{Fingerprint: fields[3], DayStart: day}
	for _, b := range cfg.Bindings {
		if !b.Enabled || b.PlatformID != fields[2] || b.CodexKeyFingerprint == "" || b.CodexKeyFingerprint != fields[3] {
			continue
		}
		mac := hmac.New(sha256.New, []byte(b.Secret))
		mac.Write([]byte("project-summary-v1\nGET\n/api/integration/codex2api/summary\n" + strings.Join(fields, "\n")))
		if hmac.Equal(sig, mac.Sum(nil)) {
			scope.Targets = append(scope.Targets, b.Target)
		}
	}
	if len(scope.Targets) == 0 {
		return nil, denied
	}
	projectSummaryNonces.Lock()
	defer projectSummaryNonces.Unlock()
	for k, expiry := range projectSummaryNonces.expires {
		if expiry < now.Unix() {
			delete(projectSummaryNonces.expires, k)
		}
	}
	key := fields[2] + ":" + fields[3] + ":" + fields[1]
	if _, used := projectSummaryNonces.expires[key]; used || len(projectSummaryNonces.expires) >= 10000 {
		return nil, denied
	}
	projectSummaryNonces.expires[key] = ts + 61
	return scope, nil
}

func (s *ProjectSummaryScope) Matches(baseURL string, keys []string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || len(keys) == 0 {
		return false
	}
	matched := false
	for _, target := range s.Targets {
		if policyTargetMatches(target, u) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	// Channel aggregates cannot distinguish keys. Mixed-key channels are excluded
	// rather than leaking traffic belonging to another binding.
	for _, key := range keys {
		digest := sha256.Sum256([]byte(strings.TrimSpace(key)))
		if hex.EncodeToString(digest[:]) != s.Fingerprint {
			return false
		}
	}
	return true
}
