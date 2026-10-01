import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  writable: true,
  value: true,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { TieredPricingEditor } = await import('../tiered-pricing-editor')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const originalAdapter = api.defaults.adapter
notifyManager.setScheduler((callback) => callback())
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => {
  notifyManager.setScheduler((callback) => setTimeout(callback, 0))
  domWindow.close()
})

test('opening holiday pricing preserves the full expression without emitting a replacement', async () => {
  const holidayExpr =
    'cn_off_peak() ? tier("off_peak", p * 4.5 + c * 13.5 + cr * 0.15) : tier("peak", p * 9 + c * 27 + cr * 0.3)'
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const changes: string[] = []
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { success: true, data: { cn_off_peak: true } },
  })
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <TieredPricingEditor
              billingExpr={holidayExpr}
              requestRuleExpr=''
              onBillingExprChange={(value) => changes.push(value)}
              onRequestRuleExprChange={() => {}}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    assert.deepEqual(changes, [])
    assert.ok(!container.textContent?.includes('cn_off_peak is not defined'))
    assert.ok(container.textContent?.includes('Hit tier: off_peak'))
    assert.ok(
      [...container.querySelectorAll('textarea')].some(
        (field) => field.value === holidayExpr
      )
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
    client.clear()
    api.defaults.adapter = originalAdapter
  }
})

const expression =
  'channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 45) : tier("base", p * 5 + c * 30)'

test('holiday discount is editable as a native time rule and its multiplier affects the estimate', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { success: true, data: { cn_off_peak: true } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const changes: string[] = []
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <TieredPricingEditor
              billingExpr='tier("base", p * 9 + c * 27)'
              requestRuleExpr='(cn_off_peak() == true ? 0.5 : 1)'
              onBillingExprChange={() => {}}
              onRequestRuleExprChange={(value) => changes.push(value)}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    assert.ok(
      container.textContent?.includes('China peak/off-peak (holidays included)')
    )
    assert.ok(
      container
        .querySelector('[aria-label="Pricing time band"]')
        ?.textContent?.includes('Off-peak')
    )
    const setter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    const tokens = container.querySelector<HTMLInputElement>(
      '#tier-estimator-prompt'
    )
    assert.ok(tokens)
    await act(async () => {
      setter.call(tokens, '2')
      tokens.dispatchEvent(
        new domWindow.Event('input', { bubbles: true }) as unknown as Event
      )
    })
    assert.ok(container.textContent?.includes('Estimated quota cost: 9'))
    const multiplier = [
      ...container.querySelectorAll<HTMLInputElement>('input'),
    ].find((input) => input.value === '0.5')
    assert.ok(multiplier)
    await act(async () => {
      setter.call(multiplier, '0.6')
      multiplier.dispatchEvent(
        new domWindow.Event('input', { bubbles: true }) as unknown as Event
      )
    })
    assert.equal(changes.at(-1), '(cn_off_peak() == true ? 0.6 : 1)')
    assert.ok(container.textContent?.includes('Estimated quota cost: 10.8'))
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.defaults.adapter = originalAdapter
  }
})

test('holiday preview waits for server time and hides stale costs if the calendar request fails', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  let finish: (() => void) | undefined
  api.defaults.adapter = (config) =>
    new Promise((resolve) => {
      finish = () =>
        resolve({
          config,
          status: 200,
          statusText: 'OK',
          headers: {},
          data: { success: true, data: { cn_off_peak: false } },
        })
    })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <TieredPricingEditor
              billingExpr='cn_off_peak() ? tier("off_peak", p * 4.5 + c * 13.5) : tier("peak", p * 9 + c * 27)'
              requestRuleExpr=''
              onBillingExprChange={() => {}}
              onRequestRuleExprChange={() => {}}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    assert.ok(
      container
        .querySelector('[role="status"]')
        ?.textContent?.includes('Loading pricing time')
    )
    assert.ok(!container.textContent?.includes('Estimated quota cost'))
    assert.ok(!container.textContent?.includes('Expression error'))
    assert.ok(finish)
    await act(async () => finish?.())
    assert.ok(container.textContent?.includes('Hit tier: peak'))
    api.defaults.adapter = async (config) => ({
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: false },
    })
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['billing-pricing-time'] })
    })
    assert.ok(
      [...container.querySelectorAll('[role="alert"]')].some((alert) =>
        alert.textContent?.includes('Pricing time is unavailable')
      )
    )
    assert.ok(!container.textContent?.includes('Estimated quota cost'))
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.defaults.adapter = originalAdapter
  }
})

test('the visual editor edits channel IDs and explains hidden marketplace tiers', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const changes: string[] = []
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <TieredPricingEditor
          billingExpr={expression}
          requestRuleExpr=''
          onBillingExprChange={(value) => changes.push(value)}
          onRequestRuleExprChange={() => {}}
        />
      </I18nextProvider>
    )
  )
  try {
    const channelInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="Channel ID condition"]'
    )
    assert.ok(channelInput)
    assert.equal(channelInput.value, '12')
    const setter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(channelInput, '15')
      channelInput.dispatchEvent(
        new domWindow.Event('input', { bubbles: true }) as unknown as Event
      )
    })
    assert.ok(changes.at(-1)?.includes('channel_id == 15'))
    assert.ok(
      container.textContent?.includes(
        'Model marketplace hides tiers when channel conditions are used.'
      )
    )
    const conditionButtons = [...container.querySelectorAll('button')].filter(
      (button) => button.textContent?.trim() === 'Add condition'
    )
    assert.equal(conditionButtons.length, 2)
    assert.equal(conditionButtons[1].disabled, true)
    assert.equal(
      container.querySelectorAll<HTMLButtonElement>(
        'button[aria-label="Remove tier"]'
      )[1].disabled,
      true
    )
    assert.equal(
      container.querySelectorAll<HTMLInputElement>('#tier-estimator-channel')
        .length,
      1
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
