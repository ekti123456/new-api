package model

import (
	"net/http"
	"testing"
)

func TestUpdateUserSessionWindowFromHeader(t *testing.T) {
	header := http.Header{}
	header.Set("X-Codex2API-Session-Limit", "5")
	header.Set("X-Codex2API-Session-Used", "3")
	header.Set("X-Codex2API-Session-Window-Seconds", "3600")
	UpdateUserSessionWindowFromHeader(12345, "https://codex-a.example", header)
	status, ok := GetUserSessionWindowStatus(12345)
	if !ok || status.Used != 3 || status.Limit != 5 || status.WindowSeconds != 3600 {
		t.Fatalf("unexpected session window status: ok=%v status=%+v", ok, status)
	}
}

func TestUserSessionWindowAggregatesDistinctTargets(t *testing.T) {
	userID := 12346
	first := http.Header{}
	first.Set("X-Codex2API-Session-Limit", "5")
	first.Set("X-Codex2API-Session-Used", "3")
	first.Set("X-Codex2API-Session-Window-Seconds", "3600")
	second := http.Header{}
	second.Set("X-Codex2API-Session-Limit", "4")
	second.Set("X-Codex2API-Session-Used", "1")
	second.Set("X-Codex2API-Session-Window-Seconds", "1800")
	UpdateUserSessionWindowFromHeader(userID, "https://codex-a.example", first)
	UpdateUserSessionWindowFromHeader(userID, "https://codex-b.example", second)
	status, ok := GetUserSessionWindowStatus(userID)
	if !ok || status.Used != 4 || status.Limit != 9 || status.WindowSeconds != 3600 {
		t.Fatalf("unexpected aggregate: ok=%v status=%+v", ok, status)
	}
}
