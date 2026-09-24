package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAPISourceVisibilityUsesAuthenticatedAdminRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const apiKey = "sk-codex2api-source-privacy"
	digest := sha256.Sum256([]byte(apiKey))
	binding := newAPIPolicyBinding{
		PlatformID: "primary-newapi", Target: "http://127.0.0.1:18095",
		CodexKeyFingerprint: hex.EncodeToString(digest[:]), Secret: "0123456789abcdef0123456789abcdef",
		Enabled: true, Profile: "balanced", Mode: "enforce",
	}
	configurePolicyTest(t, []newAPIPolicyBinding{binding})
	for _, tc := range []struct {
		name                          string
		role, status, authenticatedID int
		want                          bool
	}{
		{"admin", common2.RoleAdminUser, common2.UserStatusEnabled, 42, true},
		{"root", common2.RoleRootUser, common2.UserStatusEnabled, 42, true},
		{"ordinary_user", common2.RoleCommonUser, common2.UserStatusEnabled, 42, false},
		{"missing_role", 0, common2.UserStatusEnabled, 42, false},
		{"disabled_admin", common2.RoleAdminUser, common2.UserStatusDisabled, 42, false},
		{"different_user", common2.RoleAdminUser, common2.UserStatusEnabled, 43, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "http://newapi.example/v1/responses", nil)
			c.Request.RemoteAddr = "203.0.113.9:4567"
			c.Request.Header.Set("X-NewAPI-Preserve-Upstream-Source", "true")
			c.Request.Header.Set("X-NewAPI-User-Role", "100")
			c.Set("role", common2.RoleRootUser) // Unrelated session context is not authority.
			user := &model.UserBase{Id: tc.authenticatedID, Role: tc.role, Status: tc.status}
			user.WriteContext(c) // Same trusted cache projection used by TokenAuth.
			common2.SetContextKey(c, constant.ContextKeyUserId, tc.authenticatedID)
			body := []byte(`{"model":"gpt-6-astra","input":"hello","metadata":{"preserve_upstream_source":true,"user_role":100}}`)
			req, err := http.NewRequest(http.MethodPost, binding.Target+"/v1/responses", bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("X-NewAPI-Policy-Meta", "client-controlled")
			req.Header.Set("X-NewAPI-Policy-Meta-Signature", "client-controlled")
			info := &relaycommon.RelayInfo{
				UserId: 42, TokenId: 314, RequestId: "source-privacy-" + tc.name,
				RelayFormat:             types.RelayFormatOpenAIResponses,
				FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
				ChannelMeta:             &relaycommon.ChannelMeta{ChannelId: 7, ApiKey: apiKey},
			}
			require.NoError(t, applyNewAPIPolicyHeaders(c, req, info, bytes.NewReader(body)))
			encoded := req.Header.Get("X-NewAPI-Policy-Meta")
			payload, err := base64.RawURLEncoding.DecodeString(encoded)
			require.NoError(t, err)
			var meta newAPIPolicyMeta
			require.NoError(t, common2.Unmarshal(payload, &meta))
			assert.Equal(t, tc.want, meta.PreserveUpstreamSource)
			if !tc.want {
				assert.NotContains(t, string(payload), "preserve_upstream_source")
			}
			canonical := newAPIPolicyMetaVersion + "\n" + info.RequestId + "\n" + req.Header.Get("X-NewAPI-Body-SHA256") + "\n" + encoded
			assert.Equal(t, newAPIHMAC(binding.Secret, canonical), req.Header.Get("X-NewAPI-Policy-Meta-Signature"))
		})
	}
}
