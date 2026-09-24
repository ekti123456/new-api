/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
function isLocalHostname(hostname: string): boolean {
  return (
    hostname === 'localhost' ||
    hostname.endsWith('.localhost') ||
    /^127(?:\.\d{1,3}){3}$/.test(hostname) ||
    hostname === '[::1]' ||
    hostname === '0.0.0.0'
  )
}

export function buildCCSwitchURL(
  app: string,
  name: string,
  models: Record<string, string>,
  apiKey: string,
  serverAddress: string,
  pageOrigin?: string
): string {
  let address = serverAddress.trim().replace(/\/+$/, '')
  if (pageOrigin) {
    try {
      const configuredURL = new URL(address)
      // ServerAddress defaults to localhost on new deployments. Do not
      // export that placeholder to clients visiting a remote instance.
      if (
        !['http:', 'https:'].includes(configuredURL.protocol) ||
        (isLocalHostname(configuredURL.hostname) &&
          !isLocalHostname(new URL(pageOrigin).hostname))
      ) {
        address = pageOrigin
      }
    } catch {
      address = pageOrigin
    }
  }
  const endpoint = app === 'codex' ? `${address}/v1` : address
  const params = new URLSearchParams()
  params.set('resource', 'provider')
  params.set('app', app)
  params.set('name', app === 'codex' ? 'OpenAI' : name)
  if (app === 'codex') params.set('codexModelProvider', 'OpenAI')
  params.set('endpoint', endpoint)
  params.set('apiKey', apiKey)
  for (const [k, v] of Object.entries(models)) {
    if (v) params.set(k, v)
  }
  params.set('homepage', address)
  params.set('enabled', 'true')
  return `ccswitch://v1/import?${params.toString()}`
}
