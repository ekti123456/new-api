package common

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetRequestOriginUsesForwardedHTTPSAndOmitsPath(t *testing.T) {
	request := httptest.NewRequest("POST", "http://chat.example.com/v1/responses", nil)
	request.Header.Set("X-Forwarded-Proto", "https, http")

	require.Equal(t, "https://chat.example.com", GetRequestOrigin(request))
}

func TestGetRequestOriginPrefersDirectTLS(t *testing.T) {
	request := httptest.NewRequest("POST", "https://chat.example.com/v1/responses", nil)
	request.Header.Set("X-Forwarded-Proto", "http")

	require.Equal(t, "https://chat.example.com", GetRequestOrigin(request))
}
