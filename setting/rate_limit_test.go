package setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelRPMLimitValidationAndCompactLookup(t *testing.T) {
	require.NoError(t, CheckModelRPMRateLimitModels(`{"gpt-5.2":30}`))
	require.Error(t, CheckModelRPMRateLimitModels(`{"":30}`))
	require.Error(t, CheckModelRPMRateLimitModels(`{"gpt-5.2":0}`))
	require.Error(t, CheckModelRPMRateLimitModels(`{"gpt-5.2":2147483648}`))

	previous := ModelRPMRateLimitModels
	ModelRPMRateLimitModels = map[string]int{"gpt-5.2": 30}
	t.Cleanup(func() { ModelRPMRateLimitModels = previous })

	limit, found := GetModelRPMLimit("gpt-5.2-openai-compact")
	require.True(t, found)
	require.Equal(t, 30, limit)
}
