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
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { SettingsPageProvider } =
  await import('../../components/settings-page-context')
const { RateLimitSection } = await import('../rate-limit-section')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
after(() => domWindow.close())

test('background concurrency defaults to five, validates input and saves without changing main concurrency', async () => {
  const container = document.createElement('div')
  const actions = document.createElement('div')
  document.body.append(container, actions)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  })
  const originalAdapter = api.defaults.adapter
  const updates: unknown[] = []
  api.defaults.adapter = async (config) => {
    if (config.method === 'put' && config.url === '/api/option/') {
      updates.push(JSON.parse(config.data as string))
    }
    return {
      data: { success: true, data: [] },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <SettingsPageProvider actionsContainer={actions}>
              <RateLimitSection
                defaultValues={{
                  ModelRequestRateLimitEnabled: false,
                  ModelRPMRateLimitEnabled: false,
                  ModelRequestConcurrencyLimitEnabled: true,
                  DefaultUserConcurrencyLimit: 5,
                  BackgroundUserConcurrencyLimit: 5,
                  UserConcurrencyCooldownSeconds: 3,
                  ModelRequestRateLimitDurationMinutes: 1,
                  ModelRequestRateLimitCount: 0,
                  ModelRequestRateLimitSuccessCount: 1000,
                  ModelRequestRateLimitGroup: '{}',
                  ModelRPMRateLimitModels: '{}',
                }}
              />
            </SettingsPageProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    const label = [...container.querySelectorAll('label')].find(
      (item) => item.textContent === 'Per-user background concurrency'
    )
    assert.ok(label)
    const input = container.querySelector<HTMLInputElement>(
      `[id="${label.htmlFor}"]`
    )
    assert.ok(input)
    assert.equal(input.value, '5')
    const save = [...actions.querySelectorAll('button')].find(
      (item) => item.textContent === 'Save rate limits'
    )
    assert.ok(save)
    const setter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    for (const value of ['', '0', '1.5', '100001']) {
      await act(async () => {
        setter.call(input, value)
        input.dispatchEvent(
          new domWindow.Event('input', { bubbles: true }) as unknown as Event
        )
      })
      await act(async () => save.click())
      assert.equal(input.getAttribute('aria-invalid'), 'true', value)
      assert.deepEqual(updates, [])
    }
    await act(async () => {
      setter.call(input, '7')
      input.dispatchEvent(
        new domWindow.Event('input', { bubbles: true }) as unknown as Event
      )
    })
    await act(async () => save.click())
    assert.equal(input.getAttribute('aria-invalid'), 'false')
    assert.deepEqual(updates, [
      { key: 'BackgroundUserConcurrencyLimit', value: 7 },
    ])
  } finally {
    await act(async () => root.unmount())
    api.defaults.adapter = originalAdapter
    queryClient.clear()
    container.remove()
    actions.remove()
  }
})
