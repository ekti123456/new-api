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
import { describe, expect, it } from 'vitest'

import {
  splitBillingExprAndRequestRules,
  tryParseRequestRuleExpr,
} from '@/features/pricing/lib/billing-expr'

import fastPolicyFixture from '../../../../../../pkg/billingexpr/testdata/fast-rule-policy.json'
import { buildFastRuleSync } from '../fast-rule-sync'
import {
  importOfficialPrices,
  pricingKeys,
  type PricingOptions,
} from '../official-price-matching'

const base =
  'len > 272000 ? tier("long", p * 4 + c * 15 + cr * 0.2) : tier("base", p * 2 + c * 10 + cr * 0.1)'
const priority = '(param("service_tier") == "priority" ? 3 : 1)'
const custom = '(header("x-customer-plan") == "premium" ? 1.5 : 1)'

function options(expression = base): PricingOptions {
  return {
    ...Object.fromEntries(pricingKeys.map((key) => [key, '{}'])),
    'billing_setting.billing_mode': '{"gpt-6.2-sol":"tiered_expr"}',
    'billing_setting.billing_expr': JSON.stringify({
      'gpt-6.2-sol': expression,
    }),
  } as PricingOptions
}

function expressionOf(value: PricingOptions): string {
  return JSON.parse(value['billing_setting.billing_expr'])['gpt-6.2-sol']
}

describe('Fast rule synchronization', () => {
  it('generates exactly the policy exercised by the backend billing engine', () => {
    const result = buildFastRuleSync(options(fastPolicyFixture.base), [
      'gpt-6.2-sol',
    ])
    expect(expressionOf(result.options)).toBe(fastPolicyFixture.expression)
  })
  it('replaces acceleration rules once, preserves prices and unrelated conditions, and never adds Flex', () => {
    const snapshot = options(
      `(${base}) * ${priority} * ${priority} * ${custom}`
    )
    const result = buildFastRuleSync(snapshot, ['gpt-6.2-sol'])
    expect(result.rows).toEqual([{ name: 'gpt-6.2-sol', status: 'ready' }])
    const split = splitBillingExprAndRequestRules(expressionOf(result.options))
    expect(split.billingExpr).toBe(base)
    expect(
      tryParseRequestRuleExpr(split.requestRuleExpr)?.map((group) => [
        group.conditions[0].value,
        group.multiplier,
      ])
    ).toEqual([
      ['premium', '1.5'],
      ['fast', '2'],
      ['priority', '2'],
      ['ultrafast', '6'],
    ])
    expect(expressionOf(result.options)).not.toContain('flex')
    const repeated = buildFastRuleSync(result.options, ['gpt-6.2-sol'])
    expect(repeated.options).toEqual(result.options)
    expect(repeated.rows[0].status).toBe('unchanged')
    expect(expressionOf(snapshot)).toContain('? 3 : 1')
  })

  it('keeps explicit zero cache rates and other model prices when converting token prices', () => {
    const snapshot = {
      ...options(),
      ModelRatio: '{"gpt-6.2-sol":1,"other":9}',
      CompletionRatio: '{"gpt-6.2-sol":5}',
      CacheRatio: '{"gpt-6.2-sol":0}',
      CreateCacheRatio: '{"gpt-6.2-sol":1.25}',
      'billing_setting.billing_mode': '{}',
      'billing_setting.billing_expr': '{}',
    }
    const result = buildFastRuleSync(snapshot, ['gpt-6.2-sol', 'gpt-6.2-sol'])
    expect(result.rows).toHaveLength(1)
    expect(expressionOf(result.options)).toContain(
      'p * 2 + c * 10 + cr * 0 + cc * 2.5'
    )
    expect(result.options.ModelRatio).toBe(snapshot.ModelRatio)
    expect(result.options.CacheRatio).toBe(snapshot.CacheRatio)
    expect(JSON.parse(result.options['billing_setting.billing_mode'])).toEqual({
      'gpt-6.2-sol': 'tiered_expr',
    })
  })

  it('does not guess unpriced models, fixed charges, non-OpenAI models, or compound acceleration conditions', () => {
    const snapshot = options(
      `(${base}) * (param("service_tier") == "priority" && header("x-group") == "vip" ? 3 : 1)`
    )
    snapshot.ModelPrice = '{"gpt-5.5":0.1}'
    const result = buildFastRuleSync(snapshot, [
      'gpt-6.2-sol',
      'gpt-5.5',
      'gpt-6.1-sol',
      'claude-opus-5',
    ])
    expect(result.rows.map((row) => row.status)).toEqual([
      'manual',
      'fixed',
      'unpriced',
      'unsupported',
    ])
    expect(result.options).toEqual(snapshot)
  })

  it('uses the server effective output price for legacy locked completion ratios', () => {
    const snapshot = {
      ...options(),
      ModelRatio: '{"gpt-5.4":1}',
      CompletionRatio: '{"gpt-5.4":5}',
      completionRatioMeta: { 'gpt-5.4': { ratio: 6, locked: true } },
    }
    const result = buildFastRuleSync(snapshot, ['gpt-5.4'])
    expect(
      JSON.parse(result.options['billing_setting.billing_expr'])['gpt-5.4']
    ).toContain('p * 2 + c * 12')
  })

  it('preserves v1 and existing Flex while skipping service-tier rules embedded in raw expressions', () => {
    const snapshot = options(
      `v1:(${base}) * (param("service_tier") == "flex" ? 0.5 : 1)`
    )
    const result = buildFastRuleSync(snapshot, ['gpt-6.2-sol'])
    expect(expressionOf(result.options)).toMatch(/^v1:/)
    expect(expressionOf(result.options)).toContain('"flex" ? 0.5 : 1')
    const raw = options(
      'param("service_tier") == "fast" ? tier("fast", p * 4) : tier("base", p * 2)'
    )
    expect(buildFastRuleSync(raw, ['gpt-6.2-sol']).rows[0].status).toBe(
      'manual'
    )
  })
})

describe('price imports after Fast synchronization', () => {
  it.each([
    '',
    'len > 272000 ? tier("long", p * 8 + c * 30) : tier("base", p * 4 + c * 20)',
  ])(
    'preserves existing request rules when importing prices: %s',
    (expression) => {
      const snapshot = options(`(${base}) * ${priority} * ${custom}`)
      const result = importOfficialPrices(snapshot, [
        {
          name: 'gpt-6.2-sol',
          price: {
            provider: 'openai',
            model: 'gpt-6.2-sol',
            cost: { input: 4, output: 20 },
            expression,
          },
        },
      ])
      const split = splitBillingExprAndRequestRules(expressionOf(result))
      expect(split.billingExpr).toBe(
        expression || 'tier("base", p * 4 + c * 20)'
      )
      expect(split.requestRuleExpr).toBe(`${priority} * ${custom}`)
    }
  )
})
