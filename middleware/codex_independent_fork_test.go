package middleware

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIndependentForkDispatchAndNaming(t *testing.T) {
	for _, warm := range []bool{false, true} {
		name := "ordinary"
		if warm {
			name = "side_chat"
		}
		t.Run(name, func(t *testing.T) {
			ch, _, _ := setupCodexRootDistributorTest(t)
			parent, child, turn := uuid.NewString(), uuid.NewString(), uuid.NewString()
			const user = 982881
			// The source is recorded against a channel that is no longer present.
			require.NoError(t, service.StoreCodexRootChannelBinding(user, parent, service.CodexRootChannelBinding{ChannelID: 998899, SelectedGroup: "pro", KeyFingerprint: strings.Repeat("b", 64)}))
			c, w := codexForkedNamingContext(user, 982882, parent, child, turn, "user")
			metadata := `{"session_id":"` + child + `","thread_id":"` + child + `","forked_from_thread_id":"` + parent + `","thread_source":"user","request_kind":"turn","turn_id":"` + turn + `","root_turn_id":"` + turn + `","window_id":"` + child + `:7"}`
			body := `{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"KEEP"}],"client_metadata":` + metadata + `,"prompt_cache_key":"` + parent + `"}`
			c.Request.Body = io.NopCloser(strings.NewReader(body))
			c.Request.ContentLength = int64(len(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("X-Codex-Turn-Metadata", metadata)
			if warm {
				c.Request.Header.Set("Session-Id", parent)
			}
			Distribute()(c)
			require.False(t, c.IsAborted(), w.Body.String())
			require.Equal(t, ch.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
			resolution := relaychannel.ResolveCodexRootSessionForDistribution(c)
			require.Equal(t, child, resolution.RootID)
			require.True(t, resolution.IndependentRoot)
			require.False(t, resolution.Related)
			require.Empty(t, resolution.ForkedFromID)
			storage, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			out, err := storage.Bytes()
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(out, "client_metadata.forked_from_thread_id").Exists())
			require.Equal(t, child, c.Request.Header.Get("Session-Id"))
			require.Equal(t, child, gjson.GetBytes(out, "prompt_cache_key").String())
			require.Equal(t, gjson.Get(body, "input").Raw, gjson.GetBytes(out, "input").Raw)
			mapping, _, found, err := service.ResolveCodexThreadRootBinding(context.Background(), user, child)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, child, mapping.RootID)
			// Repeating a user fork uses its own stored Turn/Thread route.
			Distribute()(c)
			require.False(t, c.IsAborted(), w.Body.String())
			common.CleanupBodyStorage(c)
			// The naming fork carries its original graph; Codex2API resolves B.
			temporary := uuid.NewString()
			naming, nw := codexForkedNamingContext(user, 982883, child, temporary, uuid.NewString(), "thread_title")
			Distribute()(naming)
			require.False(t, naming.IsAborted(), nw.Body.String())
			require.Equal(t, temporary, relaychannel.ResolveCodexRootSessionForDistribution(naming).RootID)
			require.Equal(t, child, relaychannel.ResolveCodexRootSessionForDistribution(naming).ForkedFromID)
			require.Empty(t, relaychannel.CodexPassiveRootSessionOverrideFeature(naming))
			common.CleanupBodyStorage(naming)
		})
	}
}
