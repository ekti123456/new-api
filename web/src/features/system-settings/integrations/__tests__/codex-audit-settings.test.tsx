import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window()
const globals = [
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
] as const
for (const key of globals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { CodexAuditSettingsSection } =
  await import('../codex-audit-settings-section')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
notifyManager.setScheduler((callback) => callback())
after(() => {
  notifyManager.setScheduler((callback) => setTimeout(callback, 0))
  dom.close()
})

const initial = {
  revision: 'first',
  source: 'environment',
  cpa_supported: true,
  settings: {
    enabled: false,
    identity_forward_enabled: true,
    audit_enabled: true,
    strike_enabled: false,
    account_ban_enabled: false,
    ip_block_enabled: false,
    ban_after: 2,
    window_seconds: 86400,
    cpa_instance_id: 'stable-instance',
  },
  connections: [
    {
      id: 'one',
      target: 'https://codex.example',
      platform_id: 'gateway-a',
      api_key: '',
      secret: '',
      secret_configured: true,
      codex_key_fingerprint: 'a'.repeat(64),
      enabled: true,
    },
  ],
}

test('saving applies settings only after Save and keeps stored secrets blank in the editor', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const query = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  query.setQueryData(['codex-audit-settings'], structuredClone(initial))
  const adapter = api.defaults.adapter
  const saves: Array<typeof initial> = []
  api.defaults.adapter = async (config) => {
    let data: unknown = initial
    if (config.url?.endsWith('/status')) data = { one: { state: 'disabled' } }
    if (config.method === 'put') {
      const submitted = JSON.parse(config.data as string) as typeof initial
      saves.push(submitted)
      data = { ...submitted, revision: 'saved', source: 'database' }
    }
    return {
      data: { success: true, data, revision: 'first' },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={query}>
          <I18nextProvider i18n={i18n}>
            <CodexAuditSettingsSection />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    assert.match(container.textContent ?? '', /environment variables/)
    const passwordInputs = [
      ...container.querySelectorAll<HTMLInputElement>('input[type="password"]'),
    ]
    assert.equal(passwordInputs.length, 2)
    assert.ok(passwordInputs.every((input) => input.value === ''))
    const toggle = container.querySelector<HTMLElement>(
      '[role="switch"][aria-label="Enable Codex2API audit connection"]'
    )
    assert.ok(toggle)
    await act(async () => toggle.click())
    assert.equal(saves.length, 0)
    const save = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Save Changes'
    )
    assert.ok(save)
    await act(async () => save.click())
    assert.equal(saves.length, 1)
    assert.equal(saves[0].settings.enabled, true)
    assert.equal(saves[0].connections[0].secret, '')
    assert.equal(saves[0].connections[0].api_key, '')
    assert.match(container.textContent ?? '', /saved settings/)
  } finally {
    await act(async () => root.unmount())
    query.clear()
    api.defaults.adapter = adapter
    container.remove()
  }
})

test('failed verification shows a useful status and testing a draft never saves routing', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const query = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  query.setQueryData(['codex-audit-settings'], structuredClone(initial))
  const adapter = api.defaults.adapter
  const writes: string[] = []
  let state = 'authentication_failed'
  api.defaults.adapter = async (config) => {
    if (config.method === 'put' || config.method === 'post') {
      writes.push(config.url ?? '')
    }
    let data: unknown = initial
    if (config.url?.endsWith('/status')) data = {}
    if (config.url?.endsWith('/test')) {
      data = {
        state,
        checked_at: '2026-09-28T01:00:00Z',
        latency_ms: 12,
        http_status: state === 'connected' ? 200 : 401,
      }
    }
    return {
      data: { success: true, data, revision: 'first' },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={query}>
          <I18nextProvider i18n={i18n}>
            <CodexAuditSettingsSection />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    const button = [...container.querySelectorAll('button')].find(
      (item) => item.textContent === 'Test connection'
    )
    assert.ok(button)
    await act(async () => button.click())
    assert.match(
      container.querySelector('[role="status"]')?.textContent ?? '',
      /was rejected/
    )
    state = 'connected'
    await act(async () => button.click())
    assert.equal(
      container.querySelector('[role="status"]')?.textContent,
      'Connection verified'
    )
    assert.ok(writes.every((url) => url.endsWith('/test')))
    assert.match(container.textContent ?? '', /Last checked/)
  } finally {
    await act(async () => root.unmount())
    query.clear()
    api.defaults.adapter = adapter
    container.remove()
  }
})
