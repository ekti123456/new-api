export const codexUserAgentFamilies = [
  'Codex Desktop',
  'codex-tui',
  'codex_vscode',
  'codex_cli_rs',
  'codex_exec',
] as const

export function parseMinimumVersions(value: string): Record<string, string> {
  try {
    const parsed: unknown = JSON.parse(value || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(
        ([family, minimum]) =>
          codexUserAgentFamilies.some((known) => known === family) &&
          typeof minimum === 'string'
      )
    )
  } catch {
    return {}
  }
}

export function validMinimumVersions(
  versions: Record<string, string>
): boolean {
  return Object.values(versions).every(
    (minimum) =>
      !minimum.trim() ||
      /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/.test(
        minimum.trim()
      )
  )
}
