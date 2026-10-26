package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Existing cache contents are intentionally populated through the old cache
// APIs: a rolling deployment must ignore these entries without migrating them.
func TestCodexBackgroundIgnoresLegacyAssociationState(t *testing.T) {
	for backendIndex, backend := range []string{"memory", "redis"} {
		for index, state := range []string{"missing", "matching", "other_prefix", "other_token", "other_device", "ambiguous", "stale_channel", "legacy_alias", "expired", "cancelled"} {
			if backend == "memory" && state == "expired" {
				continue // Exercise TTL expiry with Redis's controllable clock.
			}
			t.Run(backend+"/"+state, func(t *testing.T) {
				ch, _, fingerprint := setupCodexRootDistributorTest(t)
				user, token := 996000+backendIndex*100+index, 996099
				const root = "01a17841-91b3-7373-977f-8e0cf8a738e0"
				const main = "01a17841-0000-7000-8000-000000000001"
				var observer *backgroundRootLookupObserver
				var expire func()
				if backend == "redis" {
					server, client := useCodexRecentRootRedisFixture(t, time.Now())
					observer = &backgroundRootLookupObserver{}
					client.AddHook(observer)
					expire = func() { server.FastForward(25 * time.Hour) }
				}
				c, rec := codexUnlinkedNativeTitleContext(user, token, root)
				c.Request.Header.Set("X-Codex-Installation-Id", "background-device")
				scope := service.CodexPassiveRootScope{UserID: user, TokenID: token, InstallationID: "background-device"}
				binding := service.CodexRootChannelBinding{ChannelID: ch.Id, SelectedGroup: "pro", KeyFingerprint: fingerprint}
				candidate := main
				switch state {
				case "other_prefix":
					candidate = "01a17842-0000-7000-8000-000000000001"
				case "other_token":
					scope.TokenID++
				case "other_device":
					scope.InstallationID = "other-device"
				case "stale_channel":
					binding.ChannelID += 100
				}
				if state != "missing" {
					require.NoError(t, service.StoreCodexRootChannelBinding(user, candidate, binding))
					require.NoError(t, service.StoreCodexPrefixRootCandidate(t.Context(), scope, candidate, candidate, binding))
				}
				if state == "ambiguous" {
					other := "01a17841-0000-7000-8000-000000000002"
					require.NoError(t, service.StoreCodexRootChannelBinding(user, other, binding))
					require.NoError(t, service.StoreCodexPrefixRootCandidate(t.Context(), scope, other, other, binding))
				}
				if state == "legacy_alias" {
					alias := service.CodexPassiveRootAlias{RootID: main, SelectedGroup: "pro", Association: service.CodexPrefixRootAssociation, BindingFingerprint: service.CodexRootChannelBindingFingerprint(binding)}
					require.NoError(t, service.ClaimCodexPrefixRootAlias(t.Context(), scope, root, root, alias))
					require.NoError(t, service.StoreCodexThreadRootBinding(user, root, main, binding))
				}
				if state == "expired" && expire != nil {
					expire()
				}
				if state == "cancelled" {
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				metadata := c.GetHeader("X-Codex-Turn-Metadata")
				if observer != nil {
					observer.calls.Store(0)
				}
				Distribute()(c)
				assert.False(t, c.IsAborted(), rec.Body.String())
				assert.NotContains(t, rec.Body.String(), "codex_background_root_unavailable")
				assert.Equal(t, ch.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
				assert.Equal(t, metadata, c.GetHeader("X-Codex-Turn-Metadata"))
				assert.Equal(t, root, relaychannel.ResolveCodexRootSessionForDistribution(c).RootID)
				assert.Empty(t, relaychannel.CodexPassiveRootSessionOverrideFeature(c))
				if observer != nil {
					assert.Zero(t, observer.calls.Load())
				}
				common.CleanupBodyStorage(c)
			})
		}
	}
}

func TestCodexBackgroundPreservesGraphAndDoesNotClaimUserTurn(t *testing.T) {
	for index, source := range []string{"thread_title", "thread_description", "thread_summary", "system", "subagent", "guardian_review", "agent_created_thread", "ambient_suggestions"} {
		t.Run(source, func(t *testing.T) {
			ch, _, _ := setupCodexRootDistributorTest(t)
			user, token := 996401+index, 996499
			const root = "01a17841-0000-7000-8000-000000000401"
			const turn = "01a17841-0000-7000-8000-000000000402"
			main, mainRec := codexMainRootTurnContext(user, token, ch.Id, root, turn)
			Distribute()(main)
			require.False(t, main.IsAborted(), mainRec.Body.String())
			before, found, err := service.LoadCodexTurnRootBindingContext(t.Context(), user, turn)
			require.NoError(t, err)
			require.True(t, found)
			c, rec := codexForkedNamingContext(user, token, root, "01a17841-0000-7000-8000-000000000403", turn, source)
			original := relaychannel.ResolveCodexRootSessionForDistribution(c)
			Distribute()(c)
			assert.False(t, c.IsAborted(), rec.Body.String())
			assert.Equal(t, original, relaychannel.ResolveCodexRootSessionForDistribution(c))
			after, found, err := service.LoadCodexTurnRootBindingContext(t.Context(), user, turn)
			require.NoError(t, err)
			assert.True(t, found)
			assert.Equal(t, before, after, "background requests cannot overwrite the main turn")
			common.CleanupBodyStorage(main)
			common.CleanupBodyStorage(c)
		})
	}
}

func TestCodexBackgroundRespectsCurrentUAPoolAndExplicitChannel(t *testing.T) {
	for _, scenario := range []string{"normal", "ua_pool", "removed_from_ua_pool", "specific_channel", "wrong_specific_channel"} {
		t.Run(scenario, func(t *testing.T) {
			ch, key, _ := setupCodexRootDistributorTest(t)
			base := ch.GetBaseURL()
			other := &model.Channel{Id: ch.Id + 1, Type: ch.Type, BaseURL: &base, Key: key, Status: common.ChannelStatusEnabled, Models: "gpt-5.6-sol", Group: "pro", UARoutingOnly: true}
			require.NoError(t, model.DB.Create(other).Error)
			model.InitChannelCache()
			setting := operation_setting.GetUserAgentRoutingSetting()
			original := *setting
			t.Cleanup(func() { *setting = original })
			setting.Enabled = true
			setting.GroupNames = []string{"pro"}
			setting.ChannelIDs = []int{other.Id}
			setting.UserAgentWhitelist = []string{"codex-tui"}
			c, rec := codexUnlinkedNativeTitleContext(996601, 996602, "01a17841-0000-7000-8000-000000000601")
			c.Request.Header.Set("User-Agent", "codex-tui/0.159.2")
			want := ch.Id
			switch scenario {
			case "ua_pool", "removed_from_ua_pool":
				c.Request.Header.Set("User-Agent", "other-client/1.0")
				want = other.Id
				if scenario == "removed_from_ua_pool" {
					setting.ChannelIDs = []int{other.Id + 1}
				}
			case "specific_channel", "wrong_specific_channel":
				common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(ch.Id))
				if scenario == "wrong_specific_channel" {
					c.Request.Header.Set("User-Agent", "other-client/1.0")
				}
			}
			Distribute()(c)
			if scenario == "removed_from_ua_pool" || scenario == "wrong_specific_channel" {
				assert.True(t, c.IsAborted(), rec.Body.String())
				assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyCodexBackgroundDelegated))
			} else {
				assert.False(t, c.IsAborted(), rec.Body.String())
				assert.Equal(t, want, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
			}
			common.CleanupBodyStorage(c)
		})
	}
}

func TestCodexBackgroundWebSocketKeepsConnectionChannelAndKey(t *testing.T) {
	for _, scenario := range []string{"valid", "changed_key", "unbound_key", "ordinary_model_limit", "different_channel"} {
		t.Run(scenario, func(t *testing.T) {
			ch, key, _ := setupCodexRootDistributorTest(t)
			require.NoError(t, model.DB.Model(ch).Update("setting", `{"responses_websocket_enabled":true}`).Error)
			model.InitChannelCache()
			c, rec := codexUnlinkedNativeTitleContext(996701, 996702, "01a17841-0000-7000-8000-000000000701")
			pin := ResponsesWSConnectionPin{ChannelID: ch.Id, Key: key}
			switch scenario {
			case "changed_key":
				pin.Key = "old-key"
			case "unbound_key":
				t.Setenv("CODEX2API_POLICY_BINDINGS", "[]")
			case "ordinary_model_limit":
				c.Request.Header.Set("X-Codex-Turn-Metadata", strings.ReplaceAll(c.GetHeader("X-Codex-Turn-Metadata"), "thread_title", "user"))
			case "different_channel":
				pin.ChannelID++
			}
			c.Set("responses_websocket_pin", pin)
			Distribute()(c)
			if scenario == "valid" {
				assert.False(t, c.IsAborted(), rec.Body.String())
				assert.Equal(t, ch.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
				assert.Equal(t, key, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
				assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyCodexBackgroundDelegated))
			} else {
				assert.True(t, c.IsAborted(), rec.Body.String())
				assert.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
			}
			common.CleanupBodyStorage(c)
		})
	}
}

func TestCodexBackgroundDestinationCannotChangeAfterAuthorization(t *testing.T) {
	for _, change := range []string{"key", "policy_binding"} {
		t.Run(change, func(t *testing.T) {
			ch, _, _ := setupCodexRootDistributorTest(t)
			c, rec := codexUnlinkedNativeTitleContext(996801, 996802, "01a17841-0000-7000-8000-000000000801")
			Distribute()(c)
			require.False(t, c.IsAborted(), rec.Body.String())
			if change == "key" {
				ch.Key = "changed-key"
			} else {
				t.Setenv("CODEX2API_POLICY_BINDINGS", "[]")
			}
			setupErr := SetupContextForSelectedChannel(c, ch, "gpt-5.6-luna")
			require.NotNil(t, setupErr, fmt.Sprintf("%s must be rechecked", change))
			common.CleanupBodyStorage(c)
		})
	}
}
