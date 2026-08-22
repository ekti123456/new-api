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

func TestListFullUserSessionWindowTargetsDoesNotUseAggregate(t *testing.T) {
	userID := 12347
	full := http.Header{}
	full.Set("X-Codex2API-Session-Limit", "5")
	full.Set("X-Codex2API-Session-Used", "5")
	full.Set("X-Codex2API-Session-Window-Seconds", "3600")
	available := http.Header{}
	available.Set("X-Codex2API-Session-Limit", "5")
	available.Set("X-Codex2API-Session-Used", "0")
	available.Set("X-Codex2API-Session-Window-Seconds", "3600")
	UpdateUserSessionWindowFromHeader(userID, "https://codex-full.example", full)
	UpdateUserSessionWindowFromHeader(userID, "https://codex-available.example", available)

	aggregated, ok := GetUserSessionWindowStatus(userID)
	if !ok || aggregated.Used != 5 || aggregated.Limit != 10 {
		t.Fatalf("unexpected aggregate: ok=%v status=%+v", ok, aggregated)
	}
	items := ListUserSessionWindowTargetStatuses(true)
	matched := make([]UserSessionWindowTargetStatus, 0, 1)
	for _, item := range items {
		if item.UserID == userID {
			matched = append(matched, item)
		}
	}
	if len(matched) != 1 || matched[0].Target != "https://codex-full.example" || !matched[0].Full {
		t.Fatalf("full target list=%+v, want only codex-full", matched)
	}
}
