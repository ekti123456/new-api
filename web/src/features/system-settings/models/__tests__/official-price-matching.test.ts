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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInitialLaneState } from '../model-pricing-core'
import {
  buildOfficialPriceRows,
  importOfficialPrices,
  type OfficialPrice,
  type PricingOptions,
} from '../official-price-matching'

const price: OfficialPrice = {
  provider: 'deepseek',
  model: 'deepseek-v4-flash',
  cost: { input: 10, output: 20, cache_read: 0, cache_write: 3 },
}
const options: PricingOptions = {
  ModelRatio: '{}',
  ModelPrice: '{}',
  CompletionRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  'billing_setting.billing_mode': '{}',
  'billing_setting.billing_expr': '{}',
}

test('channel prefixes and case match without changing the local model key or matching suffix aliases', () => {
  const rows = buildOfficialPriceRows(
    ['channel/DeepSeek/DEEPSEEK-V4-FLASH', 'deepseek-v4-flash-thinking'],
    [price],
    options
  )
  assert.equal(rows[0].candidate?.model, price.model)
  assert.equal(rows[0].autoSelect, true)
  assert.equal(rows[1].candidate, undefined)
})

test('different regional prices require manual selection while identical prices can share one match', () => {
  const glm = { ...price, provider: 'zai', model: 'glm-5.3' }
  const china = { ...glm, provider: 'zhipuai' }
  assert.equal(
    buildOfficialPriceRows(['glm-5.3'], [glm, china], options)[0].autoSelect,
    true
  )
  assert.equal(
    buildOfficialPriceRows(
      ['glm-5.3'],
      [glm, { ...china, cost: { input: 12, output: 20 } }],
      options
    )[0].candidate,
    undefined
  )
})

test('existing prices and expressions require selection while fixed billing stays in its editor', () => {
  const configured = {
    ...options,
    ModelRatio: '{"deepseek-v4-flash":1}',
    ModelPrice: '{"fixed":0}',
    'billing_setting.billing_expr': '{"expr":"p * 10"}',
  }
  const rows = buildOfficialPriceRows(
    ['deepseek-v4-flash', 'fixed', 'expr'],
    [price],
    configured
  )
  assert.equal(rows.find((r) => r.name === price.model)?.autoSelect, false)
  assert.equal(rows.find((r) => r.name === 'fixed')?.blocked, true)
  assert.equal(rows.find((r) => r.name === 'expr')?.blocked, false)
})

test('raw price 10 round trips as 10, including zero cache price, without currency conversion', () => {
  const updated = importOfficialPrices(options, [
    { name: 'channel/DeepSeek-V4-Flash', price },
  ])
  const name = 'channel/DeepSeek-V4-Flash'
  const result = createInitialLaneState({
    name,
    ratio: String(JSON.parse(updated.ModelRatio)[name]),
    completionRatio: String(JSON.parse(updated.CompletionRatio)[name]),
    cacheRatio: String(JSON.parse(updated.CacheRatio)[name]),
    createCacheRatio: String(JSON.parse(updated.CreateCacheRatio)[name]),
  })
  assert.equal(result.promptPrice, '10')
  assert.equal(result.prices.completion, '20')
  assert.equal(result.prices.cache, '0')
  assert.equal(result.prices.createCache, '3')
  assert.equal(options.ModelRatio, '{}')
})

test('missing optional costs preserve existing absolute prices and unrelated models', () => {
  const existing = {
    ...options,
    ModelRatio: '{"local":2,"other":99}',
    CacheRatio: '{"local":0.5}',
    AudioRatio: '{"local":2}',
    AudioCompletionRatio: '{"local":3}',
  }
  const updated = importOfficialPrices(existing, [
    { name: 'local', price: { ...price, cost: { input: 10, output: 20 } } },
  ])
  assert.deepEqual(JSON.parse(updated.ModelRatio), { local: 5, other: 99 })
  assert.equal(JSON.parse(updated.CacheRatio).local * 10, 2)
  assert.equal(JSON.parse(updated.AudioRatio).local * 10, 8)
  assert.equal(updated.AudioCompletionRatio, existing.AudioCompletionRatio)
})

test('invalid JSON, nonfinite prices, and unsupported zero-input pricing cannot be saved', () => {
  assert.throws(() =>
    buildOfficialPriceRows([], [price], { ...options, ModelRatio: 'broken' })
  )
  for (const input of [0, -1, Infinity, Number.NaN]) {
    assert.throws(() =>
      importOfficialPrices(options, [
        { name: 'local', price: { ...price, cost: { input, output: 20 } } },
      ])
    )
  }
})

test('tiered imports keep expression numbers and remove conflicting ratios for only selected models', () => {
  const expression =
    'len > 128000 ? tier("long", p * 30 + c * 40) : tier("base", p * 10 + c * 20)'
  const existing = {
    ...options,
    ModelRatio: '{"qwen":5,"other":3}',
    CacheRatio: '{"qwen":0.5}',
  }
  const updated = importOfficialPrices(existing, [
    { name: 'qwen', price: { ...price, expression } },
  ])
  assert.deepEqual(JSON.parse(updated.ModelRatio), { other: 3 })
  assert.deepEqual(JSON.parse(updated.CacheRatio), {})
  assert.deepEqual(JSON.parse(updated['billing_setting.billing_expr']), {
    qwen: expression,
  })
  assert.deepEqual(JSON.parse(updated['billing_setting.billing_mode']), {
    qwen: 'tiered_expr',
  })
})

test('regional models with equal base costs but different tiers are not auto-matched', () => {
  const sources = [
    { ...price, expression: 'p * 10' },
    {
      ...price,
      provider: 'other-region',
      expression: 'len > 1000 ? p * 20 : p * 10',
    },
  ]
  assert.equal(
    buildOfficialPriceRows([price.model], sources, options)[0].candidate,
    undefined
  )
})

test('equal numeric prices in different currencies must not be automatically merged', () => {
  const sources = [
    { ...price, currency: 'CNY' as const },
    { ...price, provider: 'international', currency: 'USD' as const },
  ]
  assert.equal(
    buildOfficialPriceRows([price.model], sources, options)[0].candidate,
    undefined
  )
})
