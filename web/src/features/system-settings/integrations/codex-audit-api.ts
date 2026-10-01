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
import { api } from '@/lib/api'

export type AuditConnection = {
  id: string
  platform_id: string
  target: string
  api_key: string
  secret: string
  secret_configured: boolean
  codex_key_fingerprint: string
  enabled: boolean
  profile?: string
  mode?: string
}

export type AuditSettings = {
  enabled: boolean
  identity_forward_enabled: boolean
  audit_enabled: boolean
  strike_enabled: boolean
  account_ban_enabled: boolean
  ip_block_enabled: boolean
  ban_after: number
  window_seconds: number
  cpa_instance_id: string
}

export type AuditEditor = {
  settings: AuditSettings
  connections: AuditConnection[]
  revision: string
  source: string
  cpa_supported: boolean
}

export type ProbeResult = {
  state: string
  checked_at?: string
  latency_ms?: number
  http_status?: number
}

type Envelope<T> = {
  success: boolean
  message?: string
  data: T
  revision?: string
}
const endpoint = '/api/option/codex2api-policy'

// Do not expose Axios request configs containing edited keys to error loggers.
async function auditRequest<T>(
  method: 'get' | 'put' | 'post',
  suffix = '',
  data?: unknown
) {
  let response: Envelope<T>
  try {
    response = (
      await api.request<Envelope<T>>({
        method,
        url: endpoint + suffix,
        data,
        timeout: 16000,
      })
    ).data
  } catch {
    throw new Error('Audit request failed. Reload settings and try again.')
  }
  if (!response.success) {
    throw new Error(response.message || 'Audit request failed')
  }
  return response
}

export async function getAuditSettings() {
  return (await auditRequest<AuditEditor>('get')).data
}
export async function saveAuditSettings(editor: AuditEditor) {
  return (await auditRequest<AuditEditor>('put', '', editor)).data
}
export async function testAuditConnection(connection: AuditConnection) {
  return (await auditRequest<ProbeResult>('post', '/test', connection)).data
}
export async function getAuditStatus() {
  return auditRequest<Record<string, ProbeResult>>('get', '/status')
}

export const blankAuditConnection: AuditConnection = {
  id: '',
  platform_id: '',
  target: '',
  api_key: '',
  secret: '',
  secret_configured: false,
  codex_key_fingerprint: '',
  enabled: true,
}
