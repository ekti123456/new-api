package channel

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestWindowTicketAndCacheBindingSeparateGatewayInstances(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	old := common.OptionMap
	common.OptionMap = map[string]string{common.GatewayInstanceIDOption: "server-a"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = old; common.OptionMapRWMutex.Unlock() })
	binding := newAPIPolicyBinding{PlatformID: "same-platform", Secret: "0123456789abcdef0123456789abcdef", Target: "https://codex.example"}
	grant := relaycommon.WindowBillingGrant{ID: "window-a", Root: "same-root", Multiplier: 1, Confirmed: true, ExpiresAt: time.Now().Add(time.Hour)}
	raw, err := common.Marshal(map[string]any{"version": 1, "platform": binding.PlatformID, "instance_id": "server-a", "user_id": "42", "root_fingerprint": "root", "grant": grant})
	require.NoError(t, err)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	ticket := encoded + "." + newAPIHMAC(binding.Secret, "codex2api-window-grant-v1\n"+encoded)
	accepted, err := verifyWindowBillingTicket(binding, "same-key", 42, "root", ticket)
	require.NoError(t, err)
	firstBinding := accepted.BindingHash
	common.OptionMapRWMutex.Lock()
	common.OptionMap[common.GatewayInstanceIDOption] = "server-b"
	common.OptionMapRWMutex.Unlock()
	_, err = verifyWindowBillingTicket(binding, "same-key", 42, "root", ticket)
	require.ErrorContains(t, err, "scope")
	require.NotEqual(t, firstBinding, windowBindingHash(binding, "same-key"), "a cached grant cannot survive an instance change")
}
