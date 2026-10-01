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

import { getSystemOptions } from '../api'
import {
  importOfficialPrices,
  parsePricingOptions,
  pricingKeys,
  type OfficialPrice,
  type PriceSelection,
  type PricingOptions,
} from './official-price-matching'

export async function getOfficialPrices(): Promise<{
  prices: OfficialPrice[]
  warnings: string[]
}> {
  const response = await api.get<{
    success: boolean
    message?: string
    data: OfficialPrice[]
    warnings?: string[]
  }>('/api/ratio_sync/official-prices', { timeout: 45000 })
  if (!response.data.success) {
    throw new Error(response.data.message || 'Failed to fetch official prices')
  }
  return { prices: response.data.data, warnings: response.data.warnings ?? [] }
}

export async function getPricingOptions(): Promise<PricingOptions> {
  const response = await getSystemOptions()
  if (!response.success) {
    throw new Error(response.message || 'Failed to load pricing settings')
  }
  const values = Object.fromEntries(
    response.data.map(({ key, value }) => [key, value])
  )
  const result = Object.fromEntries(
    pricingKeys.map((key) => [key, values[key] ?? '{}'])
  ) as PricingOptions
  parsePricingOptions(result)
  return result
}

export async function saveOfficialPrices(
  snapshot: PricingOptions,
  selections: PriceSelection[]
): Promise<void> {
  // Re-read before merging, preserving unrelated edits made since the preview.
  const current = await getPricingOptions()
  const before = parsePricingOptions(snapshot)
  const latest = parsePricingOptions(current)
  for (const { name } of selections) {
    if (pricingKeys.some((key) => before[key][name] !== latest[key][name])) {
      throw new Error(
        'Pricing changed since preview. Fetch prices again before saving.'
      )
    }
  }
  const updated = importOfficialPrices(current, selections)
  const changed = pricingKeys.filter((key) => updated[key] !== current[key])
  if (changed.length === 0) return
  const response = await api.post<{ success: boolean; message?: string }>(
    '/api/ratio_sync/official-prices/apply',
    {
      before: Object.fromEntries(changed.map((key) => [key, current[key]])),
      values: Object.fromEntries(changed.map((key) => [key, updated[key]])),
    }
  )
  if (!response.data.success) {
    throw new Error(
      response.data.message ||
        'Failed to save prices. Refresh the preview to check saved values.'
    )
  }
}
