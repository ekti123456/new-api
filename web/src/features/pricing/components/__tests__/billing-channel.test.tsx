import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { PricingModel } from '../../types'
import { DynamicPricingBreakdown } from '../dynamic-pricing-breakdown'

const domWindow = new Window()
const browserKeys = ['window', 'document', 'navigator', 'matchMedia'] as const
const originalDescriptors = browserKeys.map((key) =>
  Object.getOwnPropertyDescriptor(globalThis, key)
)
for (const key of browserKeys) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
const { ModelCard } = await import('../model-card')
const { ModelDetailsContent } = await import('../model-details')
after(() => {
  domWindow.close()
  for (const [index, key] of browserKeys.entries()) {
    const descriptor = originalDescriptors[index]
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('marketplace hides channel tiers while the administrator breakdown remains available', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', resources: { en: { translation: {} } } })
  const expression =
    'channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 45) : tier("base", p * 5 + c * 30)'
  const rendered = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <DynamicPricingBreakdown billingExpr={expression} />
    </I18nextProvider>
  )
  assert.ok(rendered.includes('Channel ID'))
  const hidden = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <DynamicPricingBreakdown billingExpr={expression} hideTiers />
    </I18nextProvider>
  )
  assert.equal(hidden, '')
})

test('hidden channel tiers use one group table with base prices and preserve priority conditions', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', resources: { en: { translation: {} } } })
  const client = new QueryClient({
    defaultOptions: { queries: { enabled: false, retry: false } },
  })
  const model: PricingModel = {
    id: 1,
    model_name: 'test-channel-model',
    model_ratio: 50,
    completion_ratio: 3,
    quota_type: 0,
    enable_groups: ['default', 'gpt-pro'],
    group_ratio: { default: 1, 'gpt-pro': 0.175 },
    billing_mode: 'tiered_expr',
    hide_tiered_pricing: true,
    billing_expr:
      'tier("base", p * 5 + c * 30 + cr * 0.5 + cc * 6.25) * (param("service_tier") == "priority" ? 2 : 1)',
  }
  try {
    const rendered = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <ModelCard model={model} onClick={() => {}} />
          <ModelDetailsContent
            model={model}
            groupRatio={{ default: 1, 'gpt-pro': 0.175 }}
            usableGroup={{
              default: { desc: 'Default', ratio: 1 },
              'gpt-pro': { desc: 'GPT Pro', ratio: 0.175 },
            }}
            endpointMap={{}}
            autoGroups={[]}
            priceRate={1}
            usdExchangeRate={1}
            tokenUnit='M'
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
    assert.match(rendered, />\$5</)
    assert.match(rendered, />\$30</)
    assert.match(rendered, />\$0\.5</)
    assert.match(rendered, />\$6\.25</)
    assert.ok(rendered.includes('Conditional multipliers'))
    assert.ok(rendered.includes('service_tier'))
    assert.ok(rendered.includes('priority'))
    assert.match(rendered, />2x</)
    assert.ok(rendered.includes('Pricing by Group'))
    assert.ok(!rendered.includes('Tiered price table'))
    assert.ok(!rendered.includes('Dynamic Pricing'))
    assert.ok(!rendered.includes('Channel ID'))
    assert.ok(!/>Tier<|>base</.test(rendered))
    const content = domWindow.document.createElement('div')
    content.innerHTML = rendered
    const groupTables = [...content.querySelectorAll('table')].filter((table) =>
      [...table.querySelectorAll('thead th')].some(
        (heading) => heading.textContent === 'Group'
      )
    )
    assert.equal(groupTables.length, 1)
    const table = groupTables[0]
    assert.deepEqual(
      [...table.querySelectorAll('thead th')].map(
        (heading) => heading.textContent
      ),
      ['Group', 'Ratio', 'Input', 'Output', 'Cache Read', 'Cache Write']
    )
    assert.equal(table.closest('section')?.querySelectorAll('table').length, 1)
    const rows = [...table.querySelectorAll('tbody tr')]
    assert.equal(rows.length, 2)
    const discountedRow = rows.find((row) =>
      row.textContent.includes('gpt-pro')
    )
    assert.ok(discountedRow)
    assert.deepEqual(
      [...discountedRow.querySelectorAll('td')]
        .slice(1)
        .map((cell) => cell.textContent),
      ['0.175x', '$0.875', '$5.25', '$0.0875', '$1.0938']
    )
  } finally {
    client.clear()
  }
})
