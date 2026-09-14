package operation_setting

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowExpansionStepDefaultPersistenceAndValidation(t *testing.T) {
	previous := windowExpansionSetting.Policy
	t.Cleanup(func() { windowExpansionSetting.Policy = previous })
	windowExpansionSetting.Policy = `{"enabled":true,"extra_limit":6,"multiplier":1.5,"channel_ids":[22]}`
	require.Equal(t, 0.1, GetWindowExpansionPolicy().MultiplierStep)
	windowExpansionSetting.Policy = `{"enabled":true,"extra_limit":6,"multiplier":1.5,"multiplier_step":0.2,"channel_ids":[22]}`
	policy := GetWindowExpansionPolicy()
	require.Equal(t, 0.2, policy.MultiplierStep)
	require.True(t, policy.Enabled)
	for _, step := range []float64{0, -0.1, 0.0000001, 10, math.NaN(), math.Inf(1)} {
		policy.MultiplierStep = step
		require.Error(t, policy.Validate())
	}
}
