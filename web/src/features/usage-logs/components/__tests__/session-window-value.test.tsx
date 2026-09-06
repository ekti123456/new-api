import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SessionWindowValue } = await import('../session-window-value')
const translations = createInstance()
await translations.use(initReactI18next).init({ lng: 'en', resources: {} })
after(() => domWindow.close())

test('window usage shows unknown, pending, or no release without inventing availability', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  for (const scenario of [
    { used: 2, nextRecoveryAt: undefined, text: 'Release time unavailable' },
    { used: 2, nextRecoveryAt: -1, text: 'Release time unavailable' },
    { used: 2, nextRecoveryAt: 1, text: 'Waiting for status update' },
    { used: 0, nextRecoveryAt: 1, text: '0/30' },
  ]) {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={translations}>
          <SessionWindowValue
            used={scenario.used}
            limit={30}
            nextRecoveryAt={scenario.nextRecoveryAt}
          />
        </I18nextProvider>
      )
    })
    assert.ok(container.textContent?.includes(scenario.text))
    assert.ok(container.textContent?.includes(`${scenario.used}/30`))
    assert.equal(container.textContent?.includes('Available'), false)
    if (scenario.used === 0) assert.equal(container.textContent, '0/30')
  }
  await act(async () => root.unmount())
})

test('release countdown ticks and keeps the reported deadline through rerenders', async (context) => {
  const nowMs = Date.parse('2026-09-06T12:00:00Z')
  context.mock.timers.enable({ apis: ['Date', 'setInterval'], now: nowMs })
  const container = document.createElement('div')
  const root = createRoot(container)
  const renderWindow = async () => {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={translations}>
          <SessionWindowValue
            used={2}
            limit={30}
            nextRecoveryAt={nowMs / 1000 + 65}
          />
        </I18nextProvider>
      )
    })
  }
  try {
    await renderWindow()
    assert.ok(container.textContent?.includes('1m 5s'))
    assert.match(
      container.querySelector('[title]')?.getAttribute('title') ?? '',
      /Estimated release:/
    )
    await act(async () => context.mock.timers.tick(5000))
    await renderWindow()
    assert.ok(container.textContent?.includes('1m'))
    assert.equal(container.textContent?.includes('1m 5s'), false)
    await act(async () => context.mock.timers.tick(60000))
    assert.ok(container.textContent?.includes('Waiting for status update'))
    assert.ok(container.textContent?.includes('2/30'))
  } finally {
    await act(async () => root.unmount())
    context.mock.timers.reset()
  }
})
