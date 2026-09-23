package kitutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaskSensitiveInfoPreservesCodexConfigGuidance(t *testing.T) {
	message := `请在配置文件 config.toml 中设置 model_provider = "OpenAI"，将配置段改为 [model_providers.OpenAI]，并设置 name = "OpenAI"。`
	require.Equal(t, message, MaskSensitiveInfo(message))
}

func TestMaskSensitiveInfoStillMasksURLsAndCredentials(t *testing.T) {
	tests := map[string]string{
		"https://config.toml/private?key=secret":     "https://***.toml/***?key=***",
		"https://api.example.com/config.toml":        "https://***.com/***",
		"api.example.com 192.168.1.1 api_key:secret": "***.***.com ***.***.***.*** api_key:***",
		"private.config.toml":                        "***.***.toml",
	}
	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, MaskSensitiveInfo(input))
		})
	}
}
