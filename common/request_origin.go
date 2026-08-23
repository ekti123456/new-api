package common

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

const maxRequestOriginBytes = 512

func sanitizeRequestOriginPart(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) <= maxRequestOriginBytes {
		return value
	}
	value = value[:maxRequestOriginBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// GetRequestOrigin returns the client-facing scheme and host without a path.
// Reverse proxies are expected to replace X-Forwarded-Proto and Host with
// trusted values before forwarding the request.
func GetRequestOrigin(request *http.Request) string {
	if request == nil {
		return ""
	}
	host := sanitizeRequestOriginPart(request.Host)
	if host == "" {
		return ""
	}

	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	} else {
		forwardedProto := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0])
		if strings.EqualFold(forwardedProto, "https") {
			scheme = "https"
		} else if strings.EqualFold(forwardedProto, "http") {
			scheme = "http"
		} else if request.URL != nil && strings.EqualFold(request.URL.Scheme, "https") {
			scheme = "https"
		}
	}

	return sanitizeRequestOriginPart(scheme + "://" + host)
}
