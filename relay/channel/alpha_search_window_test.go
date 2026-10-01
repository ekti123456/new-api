package channel

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlphaSearchReusesWindowWithoutCreatingReservation(t *testing.T) {
	for _, kind := range []string{"confirmed", "expanded", "missing", "pending", "wrong_user", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			f := newWindowReloadFixture(t)
			f.grant.Confirmed = kind != "pending"
			if kind == "expanded" {
				f.grant.Expanded, f.grant.Multiplier = true, 1.5
			}
			var controls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				controls.Add(1)
				var input WindowControlInput
				if !assert.NoError(t, common.DecodeJson(r.Body, &input)) {
					w.WriteHeader(400)
					return
				}
				assert.Equal(t, "reuse", input.Operation, "search cannot quote or release a window")
				assert.Empty(t, input.ReservationID)
				assert.False(t, input.AllowExpansion)
				if kind == "unsupported" {
					w.WriteHeader(400)
					_, _ = io.WriteString(w, `{"error":{"message":"unsupported window operation"}}`)
					return
				}
				ticket := ""
				if kind != "missing" {
					user := "42"
					if kind == "wrong_user" {
						user = "43"
					}
					ticket = f.ticket(f.grant, "", user, f.fingerprint)
				}
				body, err := common.Marshal(WindowControlResult{Version: 1, Ticket: ticket, Reason: "root_window_missing"})
				if assert.NoError(t, err) {
					_, _ = w.Write(body)
				}
			}))
			t.Cleanup(server.Close)
			f.binding.Target = server.URL
			configurePolicyTest(t, []newAPIPolicyBinding{f.binding})
			c, info := f.request()
			c.Request.URL.Path = "/v1/alpha/search"
			finish, err := PrepareWindowBilling(c, info)
			if kind == "pending" || kind == "wrong_user" || kind == "unsupported" {
				require.Error(t, err)
				assert.Nil(t, finish)
				assert.Nil(t, info.WindowBilling)
				assert.EqualValues(t, 1, controls.Load(), "never fall back to quote on an older gateway")
				return
			}
			require.NoError(t, err)
			assert.True(t, c.GetBool("window_billing_checked"))
			if kind == "missing" {
				assert.Nil(t, finish)
				assert.Nil(t, info.WindowBilling)
				assert.EqualValues(t, 1, controls.Load())
				return
			}
			require.NotNil(t, finish)
			require.NotNil(t, info.WindowBilling)
			assert.True(t, info.WindowBilling.Confirmed)
			assert.Equal(t, f.grant.ID, info.WindowBilling.ID)
			assert.True(t, f.grant.ExpiresAt.Equal(info.WindowBilling.ExpiresAt))
			assert.Equal(t, f.grant.Multiplier, info.WindowMultiplier())
			assert.Empty(t, info.WindowBilling.ReservationID)
			info.InitChannelMeta(c)
			body := `{"model":"gpt-6.1-sol","commands":{"search_query":[{"q":"test"}]}}`
			outbound, err := http.NewRequest(http.MethodPost, server.URL+"/v1/alpha/search", strings.NewReader(body))
			require.NoError(t, err)
			require.NoError(t, applyNewAPIPolicyHeaders(c, outbound, info, strings.NewReader(body)))
			encoded, err := base64.RawURLEncoding.DecodeString(outbound.Header.Get("X-NewAPI-Policy-Meta"))
			require.NoError(t, err)
			var meta newAPIPolicyMeta
			require.NoError(t, common.Unmarshal(encoded, &meta))
			assert.Equal(t, info.WindowBilling.Ticket, meta.WindowGrant, "search must actually carry the authorization")
			finish(true)
			// The next search reuses the confirmed root, with no control call.
			c, info = f.request()
			c.Request.URL.Path = "/v1/alpha/search"
			finish, err = PrepareWindowBilling(c, info)
			require.NoError(t, err)
			require.NotNil(t, finish)
			assert.EqualValues(t, 1, controls.Load())
			assert.Equal(t, f.grant.ID, info.WindowBilling.ID)
			finish(false)
			assert.EqualValues(t, 1, controls.Load(), "failure cannot release the confirmed root window")
		})
	}
}
