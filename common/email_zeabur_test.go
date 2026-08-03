package common

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func withZeaburEmailSettings(t *testing.T) {
	t.Helper()

	originalProvider := EmailProvider
	originalFrom := ZeaburEmailFrom
	originalToken := ZeaburEmailToken
	originalURL := zeaburEmailAPIURL
	originalClient := zeaburEmailClient

	t.Cleanup(func() {
		EmailProvider = originalProvider
		ZeaburEmailFrom = originalFrom
		ZeaburEmailToken = originalToken
		zeaburEmailAPIURL = originalURL
		zeaburEmailClient = originalClient
	})
}

func TestSendEmailThroughZeabur(t *testing.T) {
	withZeaburEmailSettings(t)

	const token = "test-zeabur-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var payload zeaburEmailRequest
		require.NoError(t, DecodeJson(r.Body, &payload))
		require.Equal(t, "New API <sender@example.com>", payload.From)
		require.Equal(t, []string{"first@example.com", "second@example.com"}, payload.To)
		require.Equal(t, "Verification", payload.Subject)
		require.Equal(t, "<p>123456</p>", payload.HTML)

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	EmailProvider = emailProviderZeabur
	ZeaburEmailFrom = " New API <sender@example.com> "
	ZeaburEmailToken = token
	zeaburEmailAPIURL = server.URL
	zeaburEmailClient = server.Client()

	err := SendEmail(
		"Verification",
		" first@example.com ; ; second@example.com ",
		"<p>123456</p>",
	)
	require.NoError(t, err)
}

func TestSendEmailThroughZeaburReturnsAPIError(t *testing.T) {
	withZeaburEmailSettings(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"sender is not verified"}`, http.StatusUnprocessableEntity)
	}))
	defer server.Close()

	EmailProvider = emailProviderZeabur
	ZeaburEmailFrom = "sender@example.com"
	ZeaburEmailToken = "test-zeabur-token"
	zeaburEmailAPIURL = server.URL
	zeaburEmailClient = server.Client()

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("HTTP %d", http.StatusUnprocessableEntity))
	require.Contains(t, err.Error(), "sender is not verified")
}

func TestSendEmailThroughZeaburRequiresConfiguration(t *testing.T) {
	withZeaburEmailSettings(t)

	EmailProvider = emailProviderZeabur
	ZeaburEmailToken = ""
	ZeaburEmailFrom = "sender@example.com"

	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.ErrorContains(t, err, "token is not configured")

	ZeaburEmailToken = "test-zeabur-token"
	ZeaburEmailFrom = ""
	err = SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.ErrorContains(t, err, "sender is not configured")
}

func TestSendEmailRejectsUnknownProvider(t *testing.T) {
	withZeaburEmailSettings(t)

	EmailProvider = "unknown"
	err := SendEmail("Verification", "receiver@example.com", "<p>123456</p>")
	require.ErrorContains(t, err, "unsupported email provider")
}
