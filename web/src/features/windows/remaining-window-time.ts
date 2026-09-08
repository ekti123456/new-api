export function remainingWindowTime(
  expiry: string | null | undefined,
  now: number
): string {
  const total = Math.max(0, Math.ceil((Date.parse(expiry || '') - now) / 1000))
  if (!Number.isFinite(total)) return '—'
  return `${Math.floor(total / 3600)
    .toString()
    .padStart(2, '0')}:${Math.floor((total % 3600) / 60)
    .toString()
    .padStart(2, '0')}:${(total % 60).toString().padStart(2, '0')}`
}
