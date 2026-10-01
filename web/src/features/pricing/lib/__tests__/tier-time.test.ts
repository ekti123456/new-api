import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  buildRequestRuleExpr,
  tryParseRequestRuleExpr,
  splitBillingExprAndRequestRules,
} from '../billing-expr'
import { evalExprLocally, type ExtraTokenValues } from '../tier-expr'

const expression =
  'cn_off_peak() ? tier("off_peak", p * 4.5 + c * 13.5 + cr * 0.15) : tier("peak", p * 9 + c * 27 + cr * 0.3)'

test('holiday preview uses the server time band for both peak and off-peak costs', () => {
  const extras = { cacheReadTokens: 1 } as ExtraTokenValues
  for (const [offPeak, cost, tier] of [
    [true, 18.15, 'off_peak'],
    [false, 36.3, 'peak'],
  ] as const) {
    const result = evalExprLocally(expression, 1, 1, extras, 0, offPeak)
    assert.equal(result.error, null)
    assert.equal(result.cost, cost)
    assert.equal(result.matchedTier, tier)
  }
})

test('holiday preview never guesses a price before receiving the server time band', () => {
  const result = evalExprLocally(expression, 1, 1, {} as ExtraTokenValues)
  assert.equal(result.error, 'Pricing time is unavailable')
})

test('holiday discounts round-trip through the native visual request rule editor', () => {
  const rule = '(cn_off_peak() == true ? 0.5 : 1)'
  const groups = tryParseRequestRuleExpr(rule)
  assert.ok(groups)
  assert.equal(groups[0].conditions[0].source, 'time')
  assert.equal(buildRequestRuleExpr(groups), rule)
  assert.deepEqual(
    splitBillingExprAndRequestRules(`(tier("base", p * 9 + c * 27)) * ${rule}`),
    { billingExpr: 'tier("base", p * 9 + c * 27)', requestRuleExpr: rule }
  )
  assert.equal(
    evalExprLocally(
      `(tier("base", p * 9 + c * 27)) * ${rule}`,
      1,
      1,
      {} as ExtraTokenValues,
      0,
      true
    ).cost,
    18
  )
})

test('native hour and weekday rules use their explicit timezone in the estimator', () => {
  const at = new Date('2026-09-30T01:30:00Z')
  const result = evalExprLocally(
    '(p * 9) * (hour("Asia/Shanghai") >= 9 && weekday("Asia/Shanghai") == 3 ? 0.5 : 1)',
    2,
    0,
    {} as ExtraTokenValues,
    0,
    undefined,
    at
  )
  assert.equal(result.error, null)
  assert.equal(result.cost, 9)
})

test('existing request multipliers use an empty request in the token-only preview', () => {
  const result = evalExprLocally(
    '(p * 9) * (param("service_tier") != nil && param("service_tier") == "priority" ? 2 : 1) * (header("x-plan") == "premium" ? 3 : 1)',
    2,
    0,
    {} as ExtraTokenValues
  )
  assert.equal(result.error, null)
  assert.equal(result.cost, 18)
})
