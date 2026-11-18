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
  type RequestRuleGroup,
} from '@/features/pricing/lib/billing-expr'

import {
  parsePricingOptions,
  type PricingOptions,
} from './official-price-matching'
import {
  ratioPricingExpression,
  splitPricingRules,
} from './pricing-request-rules'

export type FastRuleStatus =
  | 'ready'
  | 'unchanged'
  | 'unsupported'
  | 'fixed'
  | 'unpriced'
  | 'manual'
export type FastRuleRow = { name: string; status: FastRuleStatus }

const fastRules: RequestRuleGroup[] = [
  ['fast', '2'],
  ['priority', '2'],
  ['ultrafast', '6'],
].map(([value, multiplier]) => ({
  conditions: [{ source: 'param', path: 'service_tier', mode: 'eq', value }],
  multiplier,
}))

// This is an administrator-selected pricing policy, not a live provider price
// feed. Missing/default/auto/flex requests gain no new multiplier.
export function buildFastRuleSync(
  options: PricingOptions,
  names: string[]
): {
  options: PricingOptions
  rows: FastRuleRow[]
} {
  const maps = parsePricingOptions(options)
  const rows: FastRuleRow[] = []
  for (const name of new Set(names)) {
    const model = (name.split('/').at(-1) ?? '').toLowerCase()
    if (!/^(?:gpt-\d|chatgpt-|o[1-9](?:-|$)|codex(?:-|$))/.test(model)) {
      rows.push({ name, status: 'unsupported' })
      continue
    }
    const mode = maps['billing_setting.billing_mode'][name]
    if (mode !== 'tiered_expr' && Object.hasOwn(maps.ModelPrice, name)) {
      rows.push({ name, status: 'fixed' })
      continue
    }
    const original = maps['billing_setting.billing_expr'][name]
    const expression =
      mode === 'tiered_expr' && typeof original === 'string'
        ? original
        : ratioPricingExpression(
            maps,
            name,
            options.completionRatioMeta?.[name]?.ratio
          )
    if (!expression) {
      rows.push({ name, status: 'unpriced' })
      continue
    }
    try {
      const split = splitPricingRules(expression)
      const retained: RequestRuleGroup[] = []
      for (const group of split.groups) {
        const tierConditions = group.conditions.filter(
          (condition) =>
            condition.source !== 'time' &&
            (condition.path === 'service_tier' ||
              condition.path.toLowerCase() === 'openai-service-tier')
        )
        if (tierConditions.length === 0) {
          retained.push(group)
          continue
        }
        const condition = tierConditions[0]
        if (
          group.conditions.length !== 1 ||
          condition.source !== 'param' ||
          condition.path !== 'service_tier' ||
          condition.mode !== 'eq'
        ) {
          throw new Error('Edit complex request rules in the model editor')
        }
        if (!['fast', 'priority', 'ultrafast'].includes(condition.value)) {
          retained.push(group)
        }
      }
      const updated =
        split.version +
        combineBillingExpr(
          split.billingExpr,
          buildRequestRuleExpr([...retained, ...fastRules])
        )
      const unchanged = updated === original && mode === 'tiered_expr'
      rows.push({ name, status: unchanged ? 'unchanged' : 'ready' })
      maps['billing_setting.billing_expr'][name] = updated
      maps['billing_setting.billing_mode'][name] = 'tiered_expr'
    } catch {
      rows.push({ name, status: 'manual' })
    }
  }
  if (!rows.some((row) => row.status === 'ready')) return { options, rows }
  return {
    rows,
    options: {
      ...options,
      'billing_setting.billing_expr': JSON.stringify(
        maps['billing_setting.billing_expr']
      ),
      'billing_setting.billing_mode': JSON.stringify(
        maps['billing_setting.billing_mode']
      ),
    },
  }
}
