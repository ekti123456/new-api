# Codex2API first-response timing

NewAPI can display codex2api's loose upstream-response timing without requiring
early SSE writes. Upgrade both gateways and enable codex2api's **Report loose
first response to NewAPI** setting. It retains the legacy API setting name
`codex_preflight_sse_passthrough_enabled` but no longer enables early passthrough.
The existing trusted codex2api policy destination/key binding must be enabled.

The HTTP response contract uses `X-Codex2API-Response-Timing: v1-loose`,
`X-Codex2API-First-Response-Ms` (gateway request entry through the winning attempt's
first qualifying event), and `X-Codex2API-Attempt-First-Response-Ms` (winning
attempt only). Admission and earlier retry waits are included in the first value.
It does not include NewAPI's own routing/network time. Durations need no clock sync.

Only HTTP 200 streaming responses from the current signed policy destination,
key, request, channel and user are accepted. Unknown versions, duplicate/non-integer
headers, negative/future/oversized durations, and invalid attempt ordering fall
back to the actual first frame. The headers are stripped before downstream relay.

Usage logs preserve `other.frt` as the actual first-frame latency, and add
`other.upstream_first_response = {source: "codex2api", mode: "loose", ms, attempt_ms}`.
Desktop/mobile lists keep the compact **First token** label. Only the admin view
shows a hover with the actual first-frame time; provider/mode text is omitted.
The additional upstream-timing detail section is also admin-only. TPS, billing, retry decisions,
historical logs and existing aggregates are unaffected. Already committed headers,
older gateway versions and unsupported routes fall back to the original display.
