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
import { z } from 'zod'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export const overrideSchema = z.object({
  user_id: z.number().int().positive(),
  limit: z.number().int().min(1).max(100000),
})
export const rateRuleSchema = z
  .object({
    id: z.string().min(1),
    name: z.string().trim().min(1).max(100),
    enabled: z.boolean(),
    path: z.string().regex(/^\/[^?#*\s]*$/),
    ua_mode: z.enum(['any', 'exact', 'contains']),
    ua: z.string().max(512),
    stream: z.enum(['any', 'stream', 'non_stream']),
    limit: z.number().int().min(1).max(100000),
    overrides: z.array(overrideSchema).max(2000),
  })
  .refine((r) => r.ua_mode === 'any' || r.ua.length > 0, {
    path: ['ua'],
    message: 'UA is required for this matching mode',
  })
export const independentRateSchema = z.object({
  revision: z.string(),
  settings: z.object({
    enabled: z.boolean(),
    namespace: z.string(),
    rules: z.array(rateRuleSchema).max(64),
  }),
})
export type RateRule = z.infer<typeof rateRuleSchema>
export type UserOverride = z.infer<typeof overrideSchema>
export type RateEditor = z.infer<typeof independentRateSchema>
export type RateUsage = {
  items: Array<{
    user_id: number
    username: string
    display_name: string
    count: number
    limit: number
    retry_after: number
    allowed: boolean
  }>
  total: number
  page: number
  size: number
  backend: string
  enabled: boolean
}
type Envelope<T> = { success: boolean; message?: string; data: T }
const endpoint = '/api/option/request-rate-limits'

export async function getIndependentRates() {
  return requireServerSuccess(
    (await api.get<Envelope<RateEditor>>(endpoint)).data
  ).data
}
export async function saveIndependentRates(editor: RateEditor) {
  return requireServerSuccess(
    (await api.put<Envelope<RateEditor>>(endpoint, editor)).data
  ).data
}
export async function getIndependentUsage(
  ruleID: string,
  query: string,
  page: number
) {
  return requireServerSuccess(
    (
      await api.get<Envelope<RateUsage>>(`${endpoint}/usage`, {
        params: { rule_id: ruleID, query, page, size: 20 },
      })
    ).data
  ).data
}
export function blankRateRule(): RateRule {
  return {
    id: crypto.randomUUID(),
    name: '',
    enabled: true,
    path: '/v1/chat/completions',
    ua_mode: 'exact',
    ua: 'Go-http-client/2.0',
    stream: 'non_stream',
    limit: 100,
    overrides: [],
  }
}
