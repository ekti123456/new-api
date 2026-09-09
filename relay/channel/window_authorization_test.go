package channel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWindowAuthorizationCachesOnlyVerifiedConfirmationAndRenewsSameRoot(test *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(test.TempDir(), "user.db")), &gorm.Config{})
	require.NoError(test, err)
	previousDB, previousRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	require.NoError(test, db.AutoMigrate(&model.User{}))
	require.NoError(test, db.Create(&model.User{Id: 42, Username: "window-test", Setting: `{"window_expansion_joined":true}`}).Error)
	test.Cleanup(func() {
		model.DB, common.RedisEnabled = previousDB, previousRedis
		connection, _ := db.DB()
		_ = connection.Close()
	})
	binding := newAPIPolicyBinding{PlatformID: "window-confirmation", Secret: "0123456789abcdef0123456789abcdef", Enabled: true}
	digest := sha256.Sum256([]byte("test-key"))
	binding.CodexKeyFingerprint = hex.EncodeToString(digest[:])
	root := "11111111-1111-4111-8111-111111111111"
	fingerprint := newAPIPolicyRootSessionFingerprint(binding.PlatformID, "42", root)
	grant := relaycommon.WindowBillingGrant{ID: "grant-one", Root: "root-hash", Multiplier: 1, ExpiresAt: time.Now().Add(time.Hour), PendingUntil: time.Now().Add(30 * time.Second)}
	ticketFor := func(value relaycommon.WindowBillingGrant, owner string) string {
		raw, marshalErr := common.Marshal(map[string]any{"version": 1, "platform": binding.PlatformID, "user_id": "42", "root_fingerprint": fingerprint, "reservation_id": owner, "grant": value})
		require.NoError(test, marshalErr)
		payload := base64.RawURLEncoding.EncodeToString(raw)
		return payload + "." + newAPIHMAC(binding.Secret, "codex2api-window-grant-v1\n"+payload)
	}
	var quotes, releases atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input WindowControlInput
		if common.DecodeJson(request.Body, &input) != nil {
			writer.WriteHeader(400)
			return
		}
		if input.Operation == "release" {
			releases.Add(1)
			_, _ = writer.Write([]byte(`{"version":1}`))
			return
		}
		quotes.Add(1)
		payload, _ := common.Marshal(map[string]any{"version": 1, "ticket": ticketFor(grant, input.ReservationID)})
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	binding.Target = server.URL
	configurePolicyTest(test, []newAPIPolicyBinding{binding})
	newRequest := func() (*gin.Context, *relaycommon.RelayInfo) {
		request, _ := gin.CreateTestContext(httptest.NewRecorder())
		request.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		request.Request.RemoteAddr = "203.0.113.9:1234"
		request.Request.Header.Set("Session-Id", root)
		common.SetContextKey(request, constant.ContextKeyChannelId, 7)
		common.SetContextKey(request, constant.ContextKeyChannelBaseUrl, server.URL)
		common.SetContextKey(request, constant.ContextKeyChannelKey, "test-key")
		return request, &relaycommon.RelayInfo{UserId: 42, UserSetting: dto.UserSetting{WindowExpansionJoined: true}}
	}
	request, info := newRequest()
	finish, err := PrepareWindowBilling(request, info)
	require.NoError(test, err)
	require.NotNil(test, finish)
	require.False(test, info.WindowBilling.Confirmed)
	finish(true)
	require.EqualValues(test, 1, quotes.Load())
	require.EqualValues(test, 1, releases.Load(), "success alone cannot confirm a reservation")
	request, info = newRequest()
	finish, err = PrepareWindowBilling(request, info)
	require.NoError(test, err)
	require.EqualValues(test, 2, quotes.Load())
	info.InitChannelMeta(request)
	confirmed := grant
	confirmed.Confirmed = true
	response := &http.Response{Header: http.Header{"X-Codex2api-Window-Grant": []string{ticketFor(confirmed, info.WindowBilling.ReservationID)}}}
	require.NoError(test, acceptWindowAuthorizationResponse(request, response, info))
	require.True(test, info.WindowBilling.Confirmed)
	require.Empty(test, response.Header.Get("X-Codex2API-Window-Grant"))
	finish(true)
	request, info = newRequest()
	_, err = PrepareWindowBilling(request, info)
	require.NoError(test, err)
	require.EqualValues(test, 2, quotes.Load(), "formal authorization must avoid another control lookup")
	grant.ID, grant.Expanded, grant.Multiplier = "grant-two", true, 1.5
	_, err = RefreshWindowBilling(request, info)
	require.NoError(test, err)
	require.EqualValues(test, 3, quotes.Load())
	require.Equal(test, 1.5, info.WindowMultiplier())
	require.Equal(test, fingerprint, info.WindowBilling.Fingerprint)
	require.False(test, info.WindowBilling.Confirmed)
	info.InitChannelMeta(request)
	wrong := grant
	wrong.Confirmed, wrong.Multiplier = true, 2
	response.Header.Set("X-Codex2API-Window-Grant", ticketFor(wrong, "wrong"))
	require.Error(test, acceptWindowAuthorizationResponse(request, response, info), "confirmation cannot silently change the preauthorized price")
}
