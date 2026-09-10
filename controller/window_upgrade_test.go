package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWindowUpgradeRequiresCurrentConsentAndUsesAuthenticatedUser(test *testing.T) {
	database := setupManageUserTestDB(test)
	require.NoError(test, database.AutoMigrate(&model.Channel{}))
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	test.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	user := model.User{Id: 42, Username: "window-owner", Setting: `{"window_expansion_enabled":true,"window_expansion_accepted_ratio":1.5}`}
	require.NoError(test, database.Create(&user).Error)
	type observedControl struct {
		UserID string
		Input  relaychannel.WindowControlInput
	}
	requests := make(chan observedControl, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input relaychannel.WindowControlInput
		if common.DecodeJson(request.Body, &input) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- observedControl{UserID: request.Header.Get("X-NewAPI-User-ID"), Input: input}
		_, _ = io.WriteString(writer, `{"version":1}`)
	}))
	defer server.Close()
	channel := model.Channel{Id: 91, Name: "window-service", Type: 1, Status: 1, Key: "window-test-key", BaseURL: &server.URL}
	require.NoError(test, database.Create(&channel).Error)
	digest := sha256.Sum256([]byte(channel.Key))
	bindings, err := common.Marshal([]map[string]interface{}{{"platform_id": "window-upgrade-test", "target": server.URL, "codex_key_fingerprint": hex.EncodeToString(digest[:]), "secret": "0123456789abcdef0123456789abcdef", "enabled": true}})
	require.NoError(test, err)
	test.Setenv("CODEX2API_POLICY_ENABLED", "true")
	test.Setenv("CODEX2API_POLICY_IDENTITY_FORWARD_ENABLED", "true")
	test.Setenv("CODEX2API_POLICY_BINDINGS", string(bindings))
	var previousPolicy string
	require.NoError(test, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if key == "window_expansion_setting.policy" {
			previousPolicy = value
		}
		return nil
	}))
	test.Cleanup(func() {
		_ = config.GlobalConfig.LoadFromDB(map[string]string{"window_expansion_setting.policy": previousPolicy})
	})
	require.NoError(test, config.GlobalConfig.LoadFromDB(map[string]string{"window_expansion_setting.policy": `{"enabled":true,"extra_limit":2,"multiplier":1.5,"channel_ids":[91]}`}))
	reference, err := relaychannel.WindowServiceReference(&relaycommon.RelayInfo{UserId: 42, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: channel.Key}})
	require.NoError(test, err)
	for _, attempt := range []struct {
		multiplier float64
		pool       string
		enabled    bool
		success    bool
	}{{1, reference, true, false}, {1.5, reference, false, false}, {1.5, strings.Repeat("0", 64), true, false}, {1.5, reference, true, true}} {
		settings, encodeErr := common.Marshal(map[string]interface{}{"window_expansion_enabled": attempt.enabled, "window_expansion_accepted_ratio": 1.5})
		require.NoError(test, encodeErr)
		require.NoError(test, database.Model(&model.User{}).Where("id = ?", 42).Update("setting", string(settings)).Error)
		body, encodeErr := common.Marshal(map[string]interface{}{"pool_reference": attempt.pool, "root": "owned-root", "grant_id": "ordinary-grant", "accepted_multiplier": attempt.multiplier, "user_id": 900, "extra_limit": 99})
		require.NoError(test, encodeErr)
		recorder := httptest.NewRecorder()
		request, _ := gin.CreateTestContext(recorder)
		request.Request = httptest.NewRequest(http.MethodPost, "/api/user/self/windows/upgrade", strings.NewReader(string(body)))
		request.Request.Header.Set("Content-Type", "application/json")
		request.Set("id", 42)
		UpgradePersonalWindow(request)
		var result struct {
			Success bool `json:"success"`
		}
		require.NoError(test, common.Unmarshal(recorder.Body.Bytes(), &result))
		require.Equal(test, attempt.success, result.Success, recorder.Body.String())
		if !attempt.success {
			require.Empty(test, requests)
			continue
		}
		require.Len(test, requests, 1)
		forwarded := <-requests
		require.Equal(test, "42", forwarded.UserID)
		require.Equal(test, "upgrade", forwarded.Input.Operation)
		require.Equal(test, "owned-root", forwarded.Input.Root)
		require.Equal(test, "ordinary-grant", forwarded.Input.GrantID)
		require.Equal(test, 1.5, forwarded.Input.Multiplier)
		require.Equal(test, 2, forwarded.Input.ExtraLimit)
	}
}
