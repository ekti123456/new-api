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
export type OfficialPrice = {
  provider: string
  model: string
  currency?: 'CNY' | 'USD'
  source?: string
  source_url?: string
  verified_at?: string
  manual_only?: boolean
  expression?: string
  cost: {
    input: number
    output: number
    cache_read?: number
    cache_write?: number
  }
}

export const pricingKeys = [
  'ModelRatio',
  'ModelPrice',
  'CompletionRatio',
  'CacheRatio',
  'CreateCacheRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
  'billing_setting.billing_mode',
  'billing_setting.billing_expr',
] as const
export type PricingKey = (typeof pricingKeys)[number]
export type PricingOptions = Record<PricingKey, string>
export type PriceSelection = { name: string; price: OfficialPrice }
export type OfficialPriceRow = {
  name: string
  candidate?: OfficialPrice
  autoSelect: boolean
  blocked: boolean
  configured: boolean
  currentExpression?: string
  current: Partial<OfficialPrice['cost']>
}

export function parsePricingOptions(
  options: PricingOptions
): Record<PricingKey, Record<string, number | string>> {
  return Object.fromEntries(
    pricingKeys.map((key) => {
      const value: unknown = JSON.parse(options[key])
      if (!value || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error('Invalid pricing configuration')
      }
      return [key, value]
    })
  ) as Record<PricingKey, Record<string, number | string>>
}

export function officialPriceID(price: OfficialPrice): string {
  return `${price.provider}/${price.model}`
}

function modelNameWithoutPrefix(name: string): string {
  // Slash-delimited channel/provider prefixes only. Never remove model suffixes,
  // dates, dots, or hyphens: those can identify different billable models.
  return (name.trim().split('/').at(-1) ?? '').toLowerCase()
}

export function buildOfficialPriceRows(
  names: string[],
  prices: OfficialPrice[],
  options: PricingOptions
): OfficialPriceRow[] {
  const maps = parsePricingOptions(options)
  const index = new Map<string, OfficialPrice[]>()
  for (const price of prices) {
    const key = modelNameWithoutPrefix(price.model)
    index.set(key, [...(index.get(key) ?? []), price])
  }
  return [...new Set(names)].sort().map((name) => {
    const matches = index.get(modelNameWithoutPrefix(name)) ?? []
    const signatures = new Set(
      matches.map(({ cost, manual_only, expression, currency }) =>
        JSON.stringify([
          cost.input,
          cost.output,
          cost.cache_read,
          cost.cache_write,
          Boolean(manual_only),
          expression ?? '',
          currency ?? 'USD',
        ])
      )
    )
    const candidate = signatures.size === 1 ? matches[0] : undefined
    const blocked =
      Object.hasOwn(maps.ModelPrice, name) ||
      ['per-request'].includes(
        String(maps['billing_setting.billing_mode'][name])
      )
    const configured = pricingKeys.some((key) => Object.hasOwn(maps[key], name))
    const current: Partial<OfficialPrice['cost']> = {}
    const ratio = maps.ModelRatio[name]
    if (typeof ratio === 'number') {
      current.input = ratio * 2
      for (const [field, key] of [
        ['output', 'CompletionRatio'],
        ['cache_read', 'CacheRatio'],
        ['cache_write', 'CreateCacheRatio'],
      ] as const) {
        const value = maps[key][name]
        if (typeof value === 'number') current[field] = value * current.input
      }
    }
    return {
      name,
      candidate,
      blocked,
      configured,
      currentExpression: String(
        maps['billing_setting.billing_expr'][name] ?? ''
      ),
      current,
      autoSelect:
        Boolean(candidate) &&
        !candidate?.manual_only &&
        !configured &&
        !blocked,
    }
  })
}

export function importOfficialPrices(
  options: PricingOptions,
  selections: PriceSelection[]
): PricingOptions {
  const maps = parsePricingOptions(options)
  const changed = new Set<PricingKey>()
  for (const { name, price } of selections) {
    const { cost } = price
    if (price.manual_only) {
      throw new Error('Use the model editor for tiered or other special prices')
    }
    if (
      !name ||
      !Number.isFinite(cost.input) ||
      cost.input <= 0 ||
      !Number.isFinite(cost.output) ||
      cost.output < 0 ||
      Object.values(cost).some((value) => !Number.isFinite(value) || value < 0)
    ) {
      throw new Error('Invalid official price')
    }
    const row = buildOfficialPriceRows([name], [price], options)[0]
    if (row.blocked) {
      throw new Error('Edit fixed or expression pricing in the model editor')
    }
    if (price.expression) {
      // Expression coefficients are already prices per 1M tokens; copy them
      // verbatim. Replacing selected pricing also removes conflicting ratios.
      for (const key of pricingKeys) {
        if (Object.hasOwn(maps[key], name)) {
          delete maps[key][name]
          changed.add(key)
        }
      }
      maps['billing_setting.billing_mode'][name] = 'tiered_expr'
      maps['billing_setting.billing_expr'][name] = price.expression
      changed.add('billing_setting.billing_mode')
      changed.add('billing_setting.billing_expr')
      continue
    }
    for (const key of [
      'billing_setting.billing_mode',
      'billing_setting.billing_expr',
    ] as const) {
      if (Object.hasOwn(maps[key], name)) {
        delete maps[key][name]
        changed.add(key)
      }
    }
    const previousInput = Number(maps.ModelRatio[name]) * 2
    // Optional lanes absent from the source retain their absolute prices when
    // changing the input denominator. Audio output is relative to audio input.
    for (const key of [
      'CacheRatio',
      'CreateCacheRatio',
      'ImageRatio',
      'AudioRatio',
    ] as const) {
      const previous = maps[key][name]
      if (typeof previous === 'number' && Number.isFinite(previousInput)) {
        maps[key][name] = (previous * previousInput) / cost.input
        changed.add(key)
      }
    }
    maps.ModelRatio[name] = cost.input / 2
    maps.CompletionRatio[name] = cost.output / cost.input
    changed.add('ModelRatio')
    changed.add('CompletionRatio')
    for (const [field, key] of [
      ['cache_read', 'CacheRatio'],
      ['cache_write', 'CreateCacheRatio'],
    ] as const) {
      if (cost[field] !== undefined) {
        maps[key][name] = cost[field] / cost.input
        changed.add(key)
      }
    }
  }
  const result = { ...options }
  for (const key of changed) {
    if (
      Object.values(maps[key]).some(
        (value) =>
          typeof value === 'number' && (!Number.isFinite(value) || value < 0)
      )
    ) {
      throw new Error('Invalid pricing configuration')
    }
    result[key] = JSON.stringify(maps[key])
  }
  return result
}
