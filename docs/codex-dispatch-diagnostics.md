# Codex2API dispatch diagnostics

The administrator performance-errors dashboard can explain Codex2API `service_unavailable` failures without exposing account-selection internals to API users.

## Operator setup

1. Deploy this NewAPI receiver before updating codex2api.
2. Enable the existing signed Codex2API/NewAPI binding on both services with matching platform and secret. Signed identity alone is not enough: codex2api also requires verified policy metadata, including channel ID.
3. Keep both service clocks synchronized. Envelopes expire after 60 seconds and allow at most 10 seconds of future clock skew.
4. Keep performance metrics enabled to retain diagnostics. Find the administrator performance error by request ID; its `error_code` has the `codex_dispatch_` prefix and `error_reason` contains bounded structured JSON.

No new database column, frontend debug mode, public endpoint, or separate secret is required.

## Privacy boundary

Clients continue to receive a generic service-unavailable error and request ID. HTTP error diagnostics are removed from response headers; SSE diagnostic comments and the WS error-detail carrier are also stripped before forwarding. The encrypted carriers are never placed in regular user-facing error logs.

Only authenticated, request/user/platform-bound envelopes from the selected channel are accepted. Diagnostics are held in an attempt-specific object, so an old retry cannot overwrite the next attempt's diagnosis. A successful response cannot acquire a failure diagnostic merely by carrying a diagnostic-looking header/comment or mentioning the field name in generated text.

Administrator records include fixed reason codes and, when relevant, the strict root account ID. They exclude prompts, credentials, fingerprints, and proxy URLs. The public error object is not enriched with those fields.

Dispatch diagnostics are independent of `X-Codex2API-Policy-Violation`: they do not create policy decisions, strikes, bans, or IP blocks. Existing handling of genuine policy/upstream errors is unchanged.

## Interpretation

- `root_owner_unavailable`: the strict owner could not dispatch; inspect `reasons`, not just the summary.
- `mixed_constraints`: different candidates or evaluated gates failed for different reasons.
- `incomplete: true`: indexed selection or a state/refresh race did not expose a complete explanation; do not infer that every account failed the one observed gate.
- `stop`: skip pointless automatic channel retries.
- `backoff_same_route`: a temporary condition must not trigger channel migration. This implementation stops cross-channel retry; it does not add a sleep loop.
- `default`: preserve the existing retry policy because the diagnosis is incomplete.

Already-started stream failures can be saved to the administrator error list even when the existing stream adapter returns no outer HTTP error. This does not alter stream billing or add a second client error event.

## Retention

Details reuse `perf_metric_errors`: queries exclude records older than 48 hours and the existing cleanup loop removes expired records periodically. This is not a size cap within the 48-hour window and does not change ordinary consumption/error-log retention. If performance metrics are disabled, or storage fails, this administrator diagnostic path does not retain the details.

Wire details and the matching golden fixture are documented in codex2api's `api/docs/dispatch-diagnostics.md` and the two repositories' `dispatch_diagnostic_v1.txt` test fixtures.
