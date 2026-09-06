import assert from 'node:assert/strict'
import { test } from 'node:test'

import { parseTiersFromExpr } from '../billing-expr'
import { getDynamicPricingSummary } from '../dynamic-price'
import {
  evalExprLocally,
  generateExprFromVisualConfig,
  normalizeVisualTier,
  tryParseVisualConfig,
  type ExtraTokenValues,
} from '../tier-expr'

test('channel and context conditions round-trip and only surcharge the selected channel', () => {
  const config = {
    tiers: [
      normalizeVisualTier({
        label: 'long',
        conditions: [
          { var: 'channel_id', op: '==', value: 12 },
          { var: 'len', op: '>', value: 272000 },
        ],
        input_unit_cost: 10,
        output_unit_cost: 45,
      }),
      normalizeVisualTier({
        label: 'base',
        input_unit_cost: 5,
        output_unit_cost: 30,
      }),
    ],
  }
  const expression = generateExprFromVisualConfig(config)
  assert.equal(
    expression,
    'channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 45) : tier("base", p * 5 + c * 30)'
  )
  assert.deepEqual(tryParseVisualConfig(expression), config)
  assert.deepEqual(
    parseTiersFromExpr(expression)[0].conditions,
    config.tiers[0].conditions
  )
  for (const [channelID, length, tier] of [
    [12, 272000, 'base'],
    [12, 272001, 'long'],
    [15, 400000, 'base'],
  ] as const) {
    const result = evalExprLocally(
      expression,
      length,
      0,
      {} as ExtraTokenValues,
      channelID
    )
    assert.equal(result.error, null)
    assert.equal(result.matchedTier, tier)
  }
})

test('an empty channel draft does not silently enable a surcharge for every channel', () => {
  const expression = generateExprFromVisualConfig({
    tiers: [
      normalizeVisualTier({
        label: 'long',
        conditions: [{ var: 'channel_id', op: '==', value: '' }],
        input_unit_cost: 10,
      }),
      normalizeVisualTier({ label: 'base', input_unit_cost: 5 }),
    ],
  })
  assert.ok(expression.startsWith('channel_id == 0 ?'))
  assert.equal(
    evalExprLocally(expression, 400000, 0, {} as ExtraTokenValues, 15)
      .matchedTier,
    'base'
  )
})

test('marketplace summaries use the base price and omit tier counts for channel pricing', () => {
  const model = {
    id: 1,
    model_name: 'test',
    model_ratio: 1,
    completion_ratio: 1,
    quota_type: 0,
    enable_groups: [],
    billing_mode: 'tiered_expr',
    hide_tiered_pricing: true,
    billing_expr:
      'channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 45) : tier("base", p * 5 + c * 30)',
  }
  const summary = getDynamicPricingSummary(model, {
    tokenUnit: 'M',
    groupRatioMultiplier: 2,
  })
  assert.ok(summary)
  assert.deepEqual(
    summary.primaryEntries.map((entry) => entry.value),
    [5, 30]
  )
  assert.equal(summary.tierCount, 0)
  const unknown = getDynamicPricingSummary(
    { ...model, billing_expr: '' },
    { tokenUnit: 'M' }
  )
  assert.ok(unknown)
  assert.deepEqual(unknown.entries, [])
})
