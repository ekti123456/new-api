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
  'KeyboardEvent',
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
const { ExpansionCard } = await import('../expansion-card')
const { remainingWindowTime } = await import('../remaining-window-time')

after(() => browser.close())

test('Chinese expansion copy and switch label follow the selected locale', async (testContext) => {
  const locale = (await import('@/i18n/locales/zh.json')).default
  i18next.addResourceBundle('zh', 'translation', locale.translation)
  await i18next.changeLanguage('zh')
  const container = document.createElement('div')
  const root = createRoot(container)
  testContext.after(async () => {
    await act(async () => root.unmount())
    await i18next.changeLanguage('en')
    i18next.removeResourceBundle('zh', 'translation')
  })
  await act(async () =>
    root.render(
      <ExpansionCard
        data={{
          enabled: false,
          accepted_multiplier: 0,
          policy: {
            enabled: true,
            extra_limit: 5,
            multiplier: 1.5,
            channel_ids: [],
          },
          pools: [],
          updated_at: '',
          server_now: '',
        }}
        pending={false}
        onChange={() => undefined}
        onConfirmPrice={() => undefined}
      />
    )
  )
  assert.equal(container.querySelector('h2')?.textContent, '窗口扩容')
  assert.equal(
    container.querySelector('[role=switch]')?.getAttribute('aria-label'),
    '开启窗口扩容'
  )
  assert.match(container.textContent || '', /优先使用普通窗口，开启本身不收费/)
})

test('expansion switch supports keyboard activation and exposes the billing rules', async (testContext) => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  testContext.after(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const changes: boolean[] = []
  await act(async () =>
    root.render(
      <ExpansionCard
        data={{
          enabled: false,
          accepted_multiplier: 0,
          policy: {
            enabled: true,
            extra_limit: 5,
            multiplier: 1.5,
            channel_ids: [],
          },
          pools: [],
          updated_at: '',
          server_now: '',
        }}
        pending={false}
        onChange={(enabled) => changes.push(enabled)}
        onConfirmPrice={() => undefined}
      />
    )
  )
  const toggle = container.querySelector<HTMLElement>('[role=switch]')
  assert.ok(toggle)
  assert.equal(toggle.tabIndex, 0)
  const description = document.querySelector(
    `#${toggle.getAttribute('aria-describedby')}`
  )
  assert.match(
    description?.textContent || '',
    /Standard windows first, no surcharge for enabling/
  )
  assert.match(description?.textContent || '', /prices stay fixed until expiry/)
  await act(async () => {
    toggle.focus()
    toggle.dispatchEvent(
      new KeyboardEvent('keydown', { key: ' ', bubbles: true })
    )
    toggle.dispatchEvent(
      new KeyboardEvent('keyup', { key: ' ', bubbles: true })
    )
  })
  assert.deepEqual(changes, [true])
  assert.equal(toggle.getAttribute('aria-checked'), 'false')
})

test('disabled policy and pending saves prevent new opt-in but still allow an existing opt-in to be disabled', async (testContext) => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  testContext.after(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const changes: boolean[] = []
  const data: PersonalWindowsData = {
    enabled: false,
    accepted_multiplier: 1.5,
    policy: {
      enabled: false,
      extra_limit: 5,
      multiplier: 1.5,
      channel_ids: [],
    },
    pools: [],
    updated_at: '',
    server_now: '',
  }
  const renderCard = (pending: boolean) =>
    root.render(
      <ExpansionCard
        data={data}
        pending={pending}
        onChange={(enabled) => changes.push(enabled)}
        onConfirmPrice={() => undefined}
      />
    )
  await act(async () => renderCard(false))
  const toggle = container.querySelector<HTMLElement>('[role=switch]')
  assert.ok(toggle)
  assert.equal(toggle.getAttribute('aria-disabled'), 'true')
  await act(async () => toggle.click())
  assert.deepEqual(changes, [])
  data.enabled = true
  await act(async () => renderCard(true))
  assert.equal(toggle.getAttribute('aria-disabled'), 'true')
  assert.match(
    container.querySelector('[role=status]')?.textContent || '',
    /Saving/
  )
  await act(async () => renderCard(false))
  assert.notEqual(toggle.getAttribute('aria-disabled'), 'true')
  await act(async () => toggle.click())
  assert.deepEqual(changes, [false])
})

test('price increase and save errors keep the accepted state visible instead of implying activation at the new price', async (testContext) => {
  const container = document.createElement('div')
  const root = createRoot(container)
  testContext.after(async () => {
    await act(async () => root.unmount())
  })
  let confirmationRequested = false
  await act(async () =>
    root.render(
      <ExpansionCard
        data={{
          enabled: true,
          accepted_multiplier: 1.5,
          policy: {
            enabled: true,
            extra_limit: 5,
            multiplier: 2,
            channel_ids: [],
          },
          pools: [],
          updated_at: '',
          server_now: '',
        }}
        pending={false}
        error='Could not save expansion preference'
        onChange={() => undefined}
        onConfirmPrice={() => {
          confirmationRequested = true
        }}
      />
    )
  )
  assert.match(container.textContent || '', /Price confirmation required/)
  assert.match(
    container.querySelector('[role=alert]')?.textContent || '',
    /Could not save/
  )
  assert.equal(
    container.querySelector('[role=switch]')?.getAttribute('aria-checked'),
    'true'
  )
  const confirm = [...container.querySelectorAll('button')].find(
    (button) => button.textContent === 'Confirm updated expansion price'
  )
  assert.ok(confirm)
  await act(async () => confirm.click())
  assert.equal(confirmationRequested, true)
})

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
  assert.equal(
    container.querySelector('[aria-label="Standard windows"] dd')?.textContent,
    '1/5'
  )
  assert.equal(
    container.querySelector('[aria-label="Expanded windows"] dd')?.textContent,
    '1'
  )
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
  const toggle = container.querySelector<HTMLElement>('[role=switch]')
  assert.ok(toggle)
  assert.equal(toggle.getAttribute('aria-checked'), 'false')
  assert.equal(toggle.getAttribute('aria-label'), 'Enable expanded windows')
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
  assert.equal(toggle.getAttribute('aria-checked'), 'false')
  await act(async () => root.unmount())
  container.remove()
  client.clear()
})
