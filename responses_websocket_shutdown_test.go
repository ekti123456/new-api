package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/wsmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShutdownRelayServerWaitsForForcedSessionCleanup(t *testing.T) {
	manager := wsmanager.NewResponsesManager()
	cfg := manager.Settings()
	cfg.ShutdownSeconds = 1
	manager.Configure(cfg)
	ws, err := manager.Open(t.Context(), 1)
	require.NoError(t, err)
	ws.Upgraded()
	require.True(t, ws.BeginTurn())
	release := make(chan struct{})
	defer close(release)
	go func() { <-ws.Context().Done(); <-release; ws.Release() }()
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	go func() { done <- shutdownRelayServer(ctx, &http.Server{}, manager) }()
	select {
	case <-ws.Context().Done():
	case <-ctx.Done():
		t.Fatal("active session did not receive shutdown cancellation")
	}
	require.Equal(t, "shutdown_timeout", ws.CloseReason())
	select {
	case <-done:
		t.Fatal("shutdown returned before cleanup")
	default:
	}
	release <- struct{}{}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("shutdown did not finish")
	}
	assert.Zero(t, manager.Stats().Current)
}
