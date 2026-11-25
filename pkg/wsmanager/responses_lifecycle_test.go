package wsmanager

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesAdmissionReleasesReservationsOnFailureAndClose(t *testing.T) {
	m := NewResponsesManager()
	cfg := m.Settings()
	cfg.MaxConnections, cfg.MaxUserConnections = 2, 1
	cfg.MaxHandshakes, cfg.MaxChannelConnections = 1, 1
	m.Configure(cfg)

	a, err := m.Open(t.Context(), 10)
	require.NoError(t, err)
	_, err = m.Open(t.Context(), 10)
	require.ErrorIs(t, err, ErrUserLimit)
	b, err := m.Open(t.Context(), 20)
	require.NoError(t, err)
	_, err = m.Open(t.Context(), 30)
	require.ErrorIs(t, err, ErrConnectionLimit)
	for _, s := range []*ResponsesSession{a, b} {
		s.Upgraded()
		require.True(t, s.BeginTurn())
	}
	require.NoError(t, a.BeginDial(1))
	assert.ErrorIs(t, b.BeginDial(2), ErrHandshakeLimit)
	a.FinishDial(false)
	require.NoError(t, b.BeginDial(1))
	b.FinishDial(true)
	assert.ErrorIs(t, a.BeginDial(1), ErrChannelLimit)
	b.EndTurn()
	stats := m.Stats()
	assert.Equal(t, 2, stats.Current)
	assert.Equal(t, 1, stats.Active)
	assert.Equal(t, 1, stats.Idle)
	assert.Equal(t, 1, stats.UpstreamConnections)
	assert.EqualValues(t, 1, stats.DialErrors)
	cfg.MaxConnections = 1
	m.Configure(cfg)
	assert.Equal(t, 2, m.Stats().Current)
	b.Release()
	b.Release()
	require.NoError(t, a.BeginDial(1))
	a.Close("client_closed")
	a.FinishDial(false)
	a.Release()
	stats = m.Stats()
	assert.Zero(t, stats.Current)
	assert.Zero(t, stats.UpstreamConnections)
	assert.Zero(t, stats.UpstreamHandshakes)
	assert.Equal(t, stats.Opened, stats.Released)
	assert.EqualValues(t, 1, stats.DialCanceled)
	assert.EqualValues(t, 2, stats.ReleaseReasons["client_closed"])
	assert.Equal(t, map[string]uint64{"instance": 1, "user": 1, "channel": 1, "handshakes": 1}, stats.Rejections)
	again, err := m.Open(t.Context(), 10)
	require.NoError(t, err)
	again.Release()
}

func TestResponsesClientHandshakeLimitReleasesAtUpgrade(t *testing.T) {
	m := NewResponsesManager()
	cfg := m.Settings()
	cfg.MaxClientHandshakes = 1
	m.Configure(cfg)
	first, err := m.Open(t.Context(), 1)
	require.NoError(t, err)
	defer first.Release()
	_, err = m.Open(t.Context(), 2)
	require.ErrorIs(t, err, ErrClientHandshakeLimit)
	first.Upgraded()
	second, err := m.Open(t.Context(), 2)
	require.NoError(t, err)
	defer second.Release()
	stats := m.Stats()
	assert.Equal(t, 2, stats.Current)
	assert.Equal(t, 1, stats.MaxClientHandshakes)
	assert.Equal(t, 1, stats.ClientHandshakes)
	assert.EqualValues(t, 1, stats.Rejections["client_handshakes"])
}

func TestResponsesDrainKeepsActiveTurnUntilSettlementAndClosesIdle(t *testing.T) {
	m := NewResponsesManager()
	pending, err := m.Open(t.Context(), 1)
	require.NoError(t, err)
	activeSession, err := m.Open(t.Context(), 2)
	require.NoError(t, err)
	activeSession.Upgraded()
	require.True(t, activeSession.BeginTurn())
	drained := m.BeginDrain()
	require.ErrorIs(t, pending.Context().Err(), context.Canceled)
	require.NoError(t, activeSession.Context().Err())
	_, err = m.Open(t.Context(), 3)
	require.ErrorIs(t, err, ErrDraining)
	pending.Release()
	activeSession.EndTurn()
	require.ErrorIs(t, activeSession.Context().Err(), context.Canceled)
	assert.False(t, activeSession.BeginTurn())
	select {
	case <-drained:
		t.Fatal("drain returned before worker release")
	default:
	}
	activeSession.Release()
	select {
	case <-drained:
	default:
		t.Fatal("last release did not finish drain")
	}
	require.EqualValues(t, 2, m.Stats().ReleaseReasons["shutdown"])
}

func TestResponsesIdlePolicyAppliesToWaitingSessionsOnly(t *testing.T) {
	m := NewResponsesManager()
	waiting, err := m.Open(t.Context(), 1)
	require.NoError(t, err)
	waiting.Upgraded()
	idling, err := m.Open(t.Context(), 2)
	require.NoError(t, err)
	idling.Upgraded()
	require.True(t, idling.BeginTurn())
	idling.EndTurn()
	busy, err := m.Open(t.Context(), 3)
	require.NoError(t, err)
	busy.Upgraded()
	require.True(t, busy.BeginTurn())
	defer waiting.Release()
	defer idling.Release()
	defer busy.Release()
	cfg := m.Settings()
	require.Zero(t, cfg.FirstFrameSeconds)
	require.Zero(t, cfg.IdleSeconds)
	cfg.FirstFrameSeconds, cfg.IdleSeconds = 1, 1
	m.Configure(cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	for _, session := range []*ResponsesSession{waiting, idling} {
		select {
		case <-session.Context().Done():
		case <-ctx.Done():
			t.Fatal("waiting connection was not reclaimed")
		}
	}
	assert.Equal(t, "first_frame_timeout", waiting.CloseReason())
	assert.Equal(t, "idle_timeout", idling.CloseReason())
	assert.NoError(t, busy.Context().Err())
	cfg.IdleSeconds = 0
	m.Configure(cfg)
	busy.EndTurn()
	require.NoError(t, busy.Context().Err())
	require.True(t, busy.BeginTurn())
}

func TestResponsesSettingsRejectInvalidAndReadPartialOptions(t *testing.T) {
	cfg, err := ParseResponsesSettings(`{"max_connections":27}`)
	require.NoError(t, err)
	want := DefaultResponsesSettings()
	want.MaxConnections = 27
	assert.Equal(t, want, cfg)
	for _, raw := range []string{"invalid", "null", "[]", `{"write_seconds":0}`, `{"max_connections":null}`, `{"idle_seconds":"9"}`, `{"max_handshakes":0.5}`, `{"client_message_mib":2049}`, `{"shutdown_seconds":-1}`} {
		_, err := ParseResponsesSettings(raw)
		assert.Error(t, err, raw)
	}
}
