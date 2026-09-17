import type { LogOtherData } from '../types'

export function getReportedFirstResponse(
  timing: LogOtherData['upstream_first_response'],
  actualMs?: number
): LogOtherData['upstream_first_response'] {
  if (
    timing?.source !== 'codex2api' ||
    timing.mode !== 'loose' ||
    !Number.isSafeInteger(timing.ms) ||
    timing.ms < 0 ||
    !Number.isSafeInteger(timing.attempt_ms) ||
    timing.attempt_ms < 0 ||
    timing.attempt_ms > timing.ms ||
    actualMs == null ||
    !Number.isFinite(actualMs) ||
    timing.ms > actualMs
  ) {
    return undefined
  }
  return timing
}
