package channel

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const codexTimingHeader = "X-Codex2API-Response-Timing"
const codexFirstResponseHeader = "X-Codex2API-First-Response-Ms"
const codexAttemptFirstResponseHeader = "X-Codex2API-Attempt-First-Response-Ms"

// Timing is observational only: never change FirstResponseTime, stream state,
// retry eligibility or billing. Strip this private gateway contract downstream.
func captureCodexFirstResponse(resp *http.Response, info *relaycommon.RelayInfo, now time.Time) {
	if info != nil {
		info.CodexUpstreamFirstResponse = nil
	}
	if resp == nil {
		return
	}
	values := make(map[string][]string, 3)
	for name, value := range resp.Header {
		for _, target := range []string{codexTimingHeader, codexFirstResponseHeader, codexAttemptFirstResponseHeader} {
			if strings.EqualFold(name, target) {
				values[target] = append(values[target], value...)
				delete(resp.Header, name)
			}
		}
	}
	if info == nil || info.ChannelMeta == nil || !info.IsStream || resp.StatusCode != http.StatusOK || resp.Request == nil || resp.Request.URL == nil {
		return
	}
	if len(values[codexTimingHeader]) != 1 || values[codexTimingHeader][0] != "v1-loose" {
		return
	}
	request, ok := resp.Request.Context().Value(newAPIPolicyRequestContextKey{}).(newAPIPolicyRequestContext)
	if !ok || request.Secret == "" || request.RequestID == "" || request.ChannelID <= 0 || request.UserID <= 0 || request.RequestID != info.RequestId || request.ChannelID != info.ChannelId || request.UserID != info.UserId ||
		!IsCodex2APIPolicyDestination(resp.Request.URL.String(), info.ApiKey) {
		return
	}
	parse := func(name string) (int64, bool) {
		items := values[name]
		if len(items) != 1 || len(items[0]) == 0 || len(items[0]) > 8 || strings.IndexFunc(items[0], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return 0, false
		}
		n, err := strconv.ParseInt(items[0], 10, 64)
		return n, err == nil && n <= int64((24*time.Hour)/time.Millisecond)
	}
	ms, valid := parse(codexFirstResponseHeader)
	attemptMS, attemptValid := parse(codexAttemptFirstResponseHeader)
	if !valid || !attemptValid || attemptMS > ms || info.StartTime.IsZero() || ms > now.Sub(info.StartTime).Milliseconds() {
		return
	}
	info.CodexUpstreamFirstResponse = &relaycommon.UpstreamFirstResponseTiming{
		Source: "codex2api", Mode: "loose", Milliseconds: ms, AttemptMilliseconds: attemptMS,
	}
}
