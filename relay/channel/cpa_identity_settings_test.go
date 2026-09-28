package channel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCPAIdentityUsesSavedInstanceAndCanDisableWithoutRestart(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
	t.Setenv("CPA_IDENTITY_INSTANCE_ID", "old-environment-instance")
	s := NewAPIPolicySettings{BanAfter: 2, WindowSeconds: 86400, CPAInstanceID: "saved-instance"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for _, instance := range []string{"saved-instance", ""} {
		s.CPAInstanceID = instance
		raw, err := common.Marshal(s)
		require.NoError(t, err)
		common.OptionMapRWMutex.Lock()
		common.OptionMap[NewAPIPolicyOption] = string(raw)
		common.OptionMapRWMutex.Unlock()
		req := httptest.NewRequest("POST", "https://cpa.example/v1/responses", nil)
		require.NoError(t, applyCPAIdentity(c, req, &relaycommon.RelayInfo{UserId: 5, TokenId: 9, RequestId: "test-request"}))
		if instance == "" {
			assert.Empty(t, req.Header.Get("X-CPA-Identity"))
			continue
		}
		claim, err := base64.RawURLEncoding.DecodeString(req.Header.Get("X-CPA-Identity"))
		require.NoError(t, err)
		digest := sha256.Sum256([]byte(instance))
		assert.Equal(t, hex.EncodeToString(digest[:]), gjson.GetBytes(claim, "instance").String())
	}
}
