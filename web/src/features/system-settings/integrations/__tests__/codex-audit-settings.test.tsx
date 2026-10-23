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
const { PolicyIPManagement } = await import('../codex-policy-ip-management')
const { resources } = await import('@/i18n/config')
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

test('IP bans load on demand and releasing one IP preserves account and strike restrictions', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const query = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const adapter = api.defaults.adapter
  const requests: string[] = []
  const releases: Array<{
    ips: Array<{ ip: string; decision_id: string }>
    restore_account: boolean
    reset_strikes: boolean
    reason: string
  }> = []
  const row = {
    ip: '203.0.113.42',
    user_id: 7,
    decision_id: 'decision-1',
    request_id: 'request-1',
    reason_code: 'upstream_cyber_policy',
    created_at: 100,
    expires_at: 3700,
    revoked_at: 0,
    revoked_by: 0,
    revoke_reason: '',
  }
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    if (config.method === 'post') {
      releases.push(JSON.parse(config.data as string))
    }
    return {
      data: {
        success: true,
        data: {
          items: [row],
          total: 1,
          page_size: 50,
          server_time: 200,
          enforcement: { enabled: true, source: 'database' },
        },
      },
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
            <PolicyIPManagement />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    assert.equal(
      requests.length,
      0,
      'closed panel must not add polling or queries'
    )
    const open = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Manage IP bans'
    )
    assert.ok(open)
    await act(async () => open.click())
    assert.equal(requests.length, 1)
    assert.match(requests[0] ?? '', /status=active/)
    assert.match(container.textContent ?? '', /request-1/)
    assert.match(container.textContent ?? '', /3500s/)
    const release = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Release this IP'
    )
    assert.ok(release)
    await act(async () => release.click())
    const dialog = document.querySelector('[role="alertdialog"]')
    assert.ok(dialog)
    assert.match(
      dialog.textContent ?? '',
      /Account status will remain unchanged/
    )
    assert.equal(dialog.querySelector('#reset-policy-strikes'), null)
    const confirm = [...dialog.querySelectorAll('button')].find(
      (button) => button.textContent === 'Release restrictions'
    )
    assert.ok(confirm)
    assert.equal(confirm.disabled, true, 'a reason is required')
    const reason = dialog.querySelector<HTMLInputElement>('#release-reason')
    assert.ok(reason)
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(reason, 'reviewed')
      reason.dispatchEvent(new Event('input', { bubbles: true }))
    })
    assert.equal(confirm.disabled, false)
    await act(async () => confirm.click())
    assert.equal(releases.length, 1)
    assert.deepEqual(releases[0]?.ips, [
      { ip: row.ip, decision_id: row.decision_id },
    ])
    assert.equal(releases[0]?.restore_account, false)
    assert.equal(releases[0]?.reset_strikes, false)
    assert.equal(releases[0]?.reason, 'reviewed')
  } finally {
    await act(async () => root.unmount())
    query.clear()
    api.defaults.adapter = adapter
    container.remove()
  }
})

for (const locale of [
  {
    language: 'zhCN',
    title: 'IP 封禁管理',
    manage: '管理 IP 封禁',
    release: '仅解除此 IP',
    confirm: '解除限制',
    remaining: '剩余 3500 秒',
    unchanged: '保持账号状态不变',
    conflict: '限制或设置已变更，请刷新后重新确认。',
    future: '解封不会豁免后续违规。除非主动勾选重置，否则保留当前违规累计。',
  },
  {
    language: 'zhTW',
    title: 'IP 封禁管理',
    manage: '管理 IP 封禁',
    release: '僅解除此 IP',
    confirm: '解除限制',
    remaining: '剩餘 3500 秒',
    unchanged: '保持帳號狀態不變',
    conflict: '限制或設定已變更，請重新整理後再次確認。',
    future: '解封不會豁免後續違規。除非主動勾選重設，否則保留目前違規累計。',
  },
]) {
  test(`${locale.language} IP ban controls and release errors use shipped translations and follow language changes`, async () => {
    const localized = createInstance()
    await localized.use(initReactI18next).init({
      lng: locale.language,
      fallbackLng: 'en',
      resources,
      nsSeparator: false,
      interpolation: { escapeValue: false },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const query = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    const adapter = api.defaults.adapter
    api.defaults.adapter = async (config) => ({
      data:
        config.method === 'post'
          ? {
              success: false,
              message:
                'Restrictions or settings changed. Reload before trying again.',
            }
          : {
              success: true,
              data: {
                items: [
                  {
                    ip: '203.0.113.42',
                    user_id: 7,
                    decision_id: 'decision-1',
                    request_id: 'request-1',
                    reason_code: 'upstream_cyber_policy',
                    created_at: 100,
                    expires_at: 3700,
                    revoked_at: 0,
                    revoked_by: 0,
                    revoke_reason: '',
                  },
                ],
                total: 1,
                page_size: 50,
                server_time: 200,
                enforcement: { enabled: true, source: 'database' },
              },
            },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    try {
      await act(async () =>
        root.render(
          <QueryClientProvider client={query}>
            <I18nextProvider i18n={localized}>
              <PolicyIPManagement />
            </I18nextProvider>
          </QueryClientProvider>
        )
      )
      assert.ok(container.textContent?.includes(locale.title))
      const open = [...container.querySelectorAll('button')].find(
        (button) => button.textContent === locale.manage
      )
      assert.ok(open)
      await act(async () => open.click())
      assert.ok(container.textContent?.includes(locale.remaining))
      assert.ok(container.textContent?.includes('IP 地址'))
      const release = [...container.querySelectorAll('button')].find(
        (button) => button.textContent === locale.release
      )
      assert.ok(release)
      await act(async () => release.click())
      const dialog = document.querySelector('[role="alertdialog"]')
      assert.ok(dialog)
      assert.ok(dialog.textContent?.includes(locale.unchanged))
      assert.ok(dialog.textContent?.includes(locale.future))
      const reason = dialog.querySelector<HTMLInputElement>('#release-reason')
      assert.ok(reason)
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(setter)
      await act(async () => {
        setter.call(reason, 'reviewed')
        reason.dispatchEvent(new Event('input', { bubbles: true }))
      })
      const confirm = [...dialog.querySelectorAll('button')].find(
        (button) => button.textContent === locale.confirm
      )
      assert.ok(confirm)
      await act(async () => confirm.click())
      assert.equal(
        dialog.querySelector('[role="alert"]')?.textContent,
        locale.conflict
      )
      await act(async () => {
        await localized.changeLanguage('en')
      })
      assert.equal(
        dialog.querySelector('[role="alert"]')?.textContent,
        'Restrictions or settings changed. Reload before trying again.'
      )
      assert.ok(container.textContent?.includes('Hide IP bans'))
      await act(async () => {
        await localized.changeLanguage(locale.language)
      })
      assert.equal(
        dialog.querySelector('[role="alert"]')?.textContent,
        locale.conflict
      )
    } finally {
      await act(async () => root.unmount())
      query.clear()
      api.defaults.adapter = adapter
      container.remove()
    }
  })
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
