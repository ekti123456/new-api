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
import {
  buildRequestRuleExpr,
  combineBillingExpr,
  splitBillingExprAndRequestRules,
  tryParseRequestRuleExpr,
} from '@/features/pricing/lib/billing-expr'

import type { PricingKey } from './official-price-matching'

type PricingMaps = Record<PricingKey, Record<string, number | string>>

// Work only on the editor's standalone multipliers. Embedded request-dependent
// prices cannot be replaced without understanding the complete expression.
export function splitPricingRules(expression: string) {
  const trimmed = expression.trim()
  const version = trimmed.startsWith('v1:') ? 'v1:' : ''
  const split = splitBillingExprAndRequestRules(trimmed.slice(version.length))
  if (/\b(?:param|header|has)\s*\(|\|\|\||^v\d+:/.test(split.billingExpr)) {
    throw new Error('Edit complex request rules in the model editor')
  }
  const groups = tryParseRequestRuleExpr(split.requestRuleExpr)
  if (!groups) throw new Error('Edit complex request rules in the model editor')
  return { ...split, version, groups }
}

export function preservePricingRequestRules(
  previous: string,
  next: string
): string {
  if (!previous) return next
  const old = splitPricingRules(previous)
  if (!old.requestRuleExpr) return next
  const incoming = splitPricingRules(next)
  const incomingConditions = new Set(
    incoming.groups.map((group) => JSON.stringify(group.conditions))
  )
  const retained = old.groups.filter(
    (group) => !incomingConditions.has(JSON.stringify(group.conditions))
  )
  return (
    incoming.version +
    combineBillingExpr(
      incoming.billingExpr,
      buildRequestRuleExpr([...incoming.groups, ...retained])
    )
  )
}

export function ratioPricingExpression(
  maps: PricingMaps,
  name: string,
  effectiveCompletionRatio?: number
): string | null {
  const inputRatio = maps.ModelRatio[name]
  const completionRatio = effectiveCompletionRatio ?? maps.CompletionRatio[name]
  if (typeof inputRatio !== 'number' || typeof completionRatio !== 'number') {
    return null
  }
  const input = inputRatio * 2
  const prices: Array<[string, number]> = [
    ['p', input],
    ['c', input * completionRatio],
  ]
  for (const [key, variable] of [
    ['CacheRatio', 'cr'],
    ['CreateCacheRatio', 'cc'],
    ['ImageRatio', 'img'],
    ['AudioRatio', 'ai'],
  ] as const) {
    if (!Object.hasOwn(maps[key], name)) continue
    const ratio = maps[key][name]
    if (typeof ratio !== 'number' || ratio < 0) return null
    prices.push([variable, input * ratio])
  }
  if (Object.hasOwn(maps.AudioCompletionRatio, name)) {
    const audio = maps.AudioRatio[name]
    const output = maps.AudioCompletionRatio[name]
    if (typeof audio !== 'number' || typeof output !== 'number' || output < 0) {
      return null
    }
    prices.push(['ao', input * audio * output])
  }
  if (
    inputRatio < 0 ||
    completionRatio < 0 ||
    prices.some(([, value]) => !Number.isFinite(value) || value < 0)
  ) {
    return null
  }
  return `tier("base", ${prices.map(([key, value]) => `${key} * ${value}`).join(' + ')})`
}
