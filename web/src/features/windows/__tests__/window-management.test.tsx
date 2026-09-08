import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { PersonalWindowsData, WindowPool } from '../types'

const browser = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
  'sessionStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: browser[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
  fallbackLng: 'en',
})
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { PersonalWindows } = await import('../index')
const { WindowPoolCard } = await import('../window-pool-card')
const { remainingWindowTime } = await import('../remaining-window-time')

after(() => browser.close())

test('unavailable pool reports unknown usage rather than an empty window list', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const pool: WindowPool = {
    id: 1,
    name: 'Pool',
    available: false,
    status: { limit: 5, used: 0, windows: null, cooldown_unavailable: true },
  }
  await act(async () =>
    root.render(<WindowPoolCard pool={pool} now={Date.now()} />)
  )
  assert.match(
    container.querySelector('[role=alert]')?.textContent || '',
    /Unavailable does not mean zero usage/
  )
  assert.doesNotMatch(container.textContent || '', /No active windows/)
  await act(async () => root.unmount())
})

test('window list separates tariffs and removes expired windows without a network request', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const now = Date.parse('2026-09-08T10:00:00Z')
  const pool: WindowPool = {
    id: 1,
    name: 'Pool',
    available: true,
    status: {
      limit: 5,
      used: 3,
      cooldown_unavailable: false,
      windows: [
        {
          id: 'ordinary',
          created_at: '2026-09-08T09:00:00Z',
          expires_at: '2026-09-08T11:00:00Z',
          expanded: false,
          multiplier: 1,
        },
        {
          id: 'paid',
          created_at: '2026-09-08T09:00:00Z',
          expires_at: '2026-09-08T11:00:00Z',
          expanded: true,
          multiplier: 1.5,
        },
        {
          id: 'expired',
          created_at: '2026-09-08T09:00:00Z',
          expires_at: '2026-09-08T09:59:59Z',
          expanded: false,
          multiplier: 1,
        },
      ],
    },
  }
  await act(async () => root.render(<WindowPoolCard pool={pool} now={now} />))
  assert.equal(container.querySelectorAll('article').length, 2)
  assert.match(container.textContent || '', /Standard windows: 1\/5/)
  assert.match(container.textContent || '', /Expanded windows: 1/)
  assert.match(container.textContent || '', /×1.5/)
  assert.equal(remainingWindowTime('2026-09-08T11:00:00Z', now), '01:00:00')
  await act(async () =>
    root.render(<WindowPoolCard pool={pool} now={now + 3600000} />)
  )
  assert.equal(container.querySelectorAll('article').length, 0)
  assert.match(container.textContent || '', /No active windows/)
  await act(async () => root.unmount())
})

test('enabling expansion requires confirming the displayed price and cancel leaves it disabled', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const data: PersonalWindowsData = {
    enabled: false,
    accepted_multiplier: 0,
    policy: {
      enabled: true,
      extra_limit: 5,
      multiplier: 1.5,
      channel_ids: [1],
    },
    pools: [],
    updated_at: new Date().toISOString(),
    server_now: new Date().toISOString(),
  }
  client.setQueryData(['personal-windows', undefined], data)
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <PersonalWindows />
      </QueryClientProvider>
    )
  )
  const toggle = container.querySelector<HTMLInputElement>('[role=switch]')
  assert.ok(toggle)
  await act(async () => toggle.click())
  assert.match(
    document.querySelector('[role=alertdialog]')?.textContent || '',
    /×1.5/
  )
  const cancel = [...document.querySelectorAll('button')].find(
    (button) => button.textContent === 'Cancel'
  )
  assert.ok(cancel)
  await act(async () => cancel.click())
  assert.equal(toggle.checked, false)
  await act(async () => root.unmount())
  container.remove()
  client.clear()
})
