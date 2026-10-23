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
import { isAxiosError } from 'axios'

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
  } catch (error) {
    if (isAxiosError(error) && error.response?.status === 409) {
      throw new Error(
        'Restrictions or settings changed. Reload before trying again.'
      )
    }
    if (
      isAxiosError(error) &&
      error.response?.status === 400 &&
      ['/ip-blocks', '/restrictions', '/release'].some((path) =>
        suffix.startsWith(path)
      )
    ) {
      throw new Error('Invalid IP address, user ID or release parameters.')
    }
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

export type PolicyIPBlock = {
  ip: string
  user_id: number
  decision_id: string
  request_id: string
  reason_code: string
  created_at: number
  expires_at: number
  revoked_at: number
  revoked_by: number
  revoke_reason: string
}
type IPEnforcement = { enabled: boolean; source: string }
export type PolicyIPPage = {
  items: PolicyIPBlock[]
  total: number
  page_size: number
  server_time: number
  enforcement: IPEnforcement
}
export type PolicyRestrictionDetails = {
  server_time: number
  enforcement: IPEnforcement
  ip?: string
  ip_block?: PolicyIPBlock
  login_limit_error?: boolean
  login_limit?: {
    enabled: boolean
    blocked: boolean
    scope: string
    count: number
    limit: number
    remaining_seconds: number
  }
  user?: {
    user_id: number
    username: string
    user_status: number
    user_auth_version: number
    policy_disabled: boolean
    state: { revision: number; reset_after_id: number }
    active_strikes: number
    active_ips: PolicyIPBlock[]
    active_ip_count: number
    events: Array<{
      id: number
      ip: string
      action: string
      actor_id: number
      created_at: number
      reason: string
    }>
  }
}
export type PolicyRelease = {
  user_id: number
  ips: Array<{ ip: string; decision_id: string }>
  state_revision?: number
  user_auth_version?: number
  restore_account: boolean
  reset_strikes: boolean
  reason: string
}
export async function getPolicyIPBlocks(params: Record<string, string>) {
  return (
    await auditRequest<PolicyIPPage>(
      'get',
      `/ip-blocks?${new URLSearchParams(params)}`
    )
  ).data
}
export async function getPolicyRestrictions(params: Record<string, string>) {
  return (
    await auditRequest<PolicyRestrictionDetails>(
      'get',
      `/restrictions?${new URLSearchParams(params)}`
    )
  ).data
}
export async function releasePolicyRestrictions(input: PolicyRelease) {
  return auditRequest('post', '/release', input)
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
