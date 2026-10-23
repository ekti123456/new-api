package common

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

const CodexPolicyOption = "Codex2APIPolicySecret"

type CodexPolicyIPEnforcement struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
}

type codexPolicyIPSnapshot struct {
	raw    string
	config CodexPolicyIPEnforcement
	err    error
}

var codexPolicyIPConfig atomic.Pointer[codexPolicyIPSnapshot]

// Read the same saved option used to create penalties. Parse only on change;
// request authentication must not decode the (secret-bearing) settings blob.
// The IP switch controls existing penalties independently of audit collection.
func ReadCodexPolicyIPEnforcement() (CodexPolicyIPEnforcement, error) {
	OptionMapRWMutex.RLock()
	raw, saved := OptionMap[CodexPolicyOption]
	OptionMapRWMutex.RUnlock()
	if !saved {
		value := strings.TrimSpace(os.Getenv("CODEX2API_POLICY_IP_BLOCK_ENABLED"))
		if value == "" {
			value = "false"
		}
		enabled, err := strconv.ParseBool(value)
		return CodexPolicyIPEnforcement{Enabled: enabled, Source: "environment"}, err
	}
	if cached := codexPolicyIPConfig.Load(); cached != nil && cached.raw == raw {
		return cached.config, cached.err
	}
	var settings struct {
		IPBlockEnabled bool `json:"ip_block_enabled"`
	}
	snapshot := &codexPolicyIPSnapshot{raw: raw, config: CodexPolicyIPEnforcement{Source: "database"}}
	if err := UnmarshalJsonStr(raw, &settings); err != nil {
		snapshot.err = errors.New("invalid saved IP enforcement settings")
	} else {
		snapshot.config.Enabled = settings.IPBlockEnabled
	}
	codexPolicyIPConfig.Store(snapshot)
	return snapshot.config, snapshot.err
}
