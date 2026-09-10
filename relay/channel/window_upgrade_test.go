package channel

import (
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestWindowUpgradeInvalidatesOnlyUsersFutureTariffs(test *testing.T) {
	previous := windowBillingCache
	windowBillingCache = newWindowGrantCache(8)
	test.Cleanup(func() { windowBillingCache = previous })
	grant := relaycommon.WindowBillingGrant{ID: "ordinary", Multiplier: 1, Confirmed: true, ExpiresAt: time.Now().Add(time.Hour)}
	windowBillingCache.putLocked("binding:42:root-one", cachedWindowGrant{grant: grant})
	windowBillingCache.putLocked("binding:42:root-two", cachedWindowGrant{grant: grant})
	windowBillingCache.putLocked("binding:142:root-other", cachedWindowGrant{grant: grant})
	inFlight := grant
	InvalidateUserWindowBilling(42)
	require.Len(test, windowBillingCache.items, 1)
	require.Contains(test, windowBillingCache.items, "binding:142:root-other")
	require.Equal(test, 1.0, inFlight.Multiplier)
	require.Equal(test, "ordinary", inFlight.ID)
}
