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
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const browser = new Window({ url: 'http://localhost' })
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
  'ResizeObserver',
  'Document',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: browser[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  writable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { OfficialPriceSync } = await import('../official-price-sync')
const { pricingKeys } = await import('../official-price-matching')
const { saveOfficialPrices } = await import('../official-price-api')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  interpolation: { escapeValue: false },
  resources: { en: { translation: {} } },
})
const originalAdapter = api.defaults.adapter
notifyManager.setScheduler(queueMicrotask)
after(() => {
  api.defaults.adapter = originalAdapter
  notifyManager.setScheduler((callback) => setTimeout(callback, 0))
  browser.close()
})

test('preview selects only unpriced exact matches, supports manual selection, and disables stale preview after fetch failure', async () => {
  let fail = false
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    let data: unknown
    if (config.url === '/api/ratio_sync/official-prices') {
      data = fail
        ? { success: false, message: 'Failed to fetch official prices' }
        : {
            success: true,
            data: [
              {
                provider: 'deepseek',
                model: 'deepseek-v4-flash',
                cost: { input: 10, output: 20 },
              },
            ],
          }
    } else if (config.url === '/api/channel/models_enabled') {
      data = {
        success: true,
        data: ['channel/DEEPSEEK-V4-FLASH', 'deepseek-v4-flash', 'unknown'],
      }
    } else if (config.url === '/api/option/') {
      data = {
        success: true,
        data: pricingKeys.map((key) => ({
          key,
          value: key === 'ModelRatio' ? '{"deepseek-v4-flash":1}' : '{}',
        })),
      }
    } else {
      throw new Error(`Unexpected request: ${config.url}`)
    }
    return { config, data, status: 200, statusText: 'OK', headers: {} }
  }
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false, gcTime: 0 },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const fetchPreview = async () => {
    await act(async () => {
      const done = new Promise<void>((resolve) => {
        const unsubscribe = client.getMutationCache().subscribe((event) => {
          if (
            event.mutation?.state.status === 'success' ||
            event.mutation?.state.status === 'error'
          ) {
            unsubscribe()
            resolve()
          }
        })
      })
      const button = [...container.querySelectorAll('button')].find(
        (item) => item.textContent === 'Fetch official prices'
      )
      assert.ok(button)
      button.click()
      await done
    })
  }
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <OfficialPriceSync />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    await fetchPreview()
    const automatic = container.querySelector<HTMLInputElement>(
      'input[aria-label="Sync channel/DEEPSEEK-V4-FLASH"]'
    )
    const existing = container.querySelector<HTMLInputElement>(
      'input[aria-label="Sync deepseek-v4-flash"]'
    )
    const missing = container.querySelector<HTMLInputElement>(
      'input[aria-label="Sync unknown"]'
    )
    assert.ok(automatic && existing && missing)
    assert.equal(automatic.checked, true)
    assert.equal(existing.checked, false)
    assert.equal(missing.disabled, true)
    assert.ok(container.textContent?.includes('10 / 20 / — / —'))
    await act(async () => existing.click())
    assert.equal(existing.checked, true)
    assert.ok(container.textContent?.includes('Save selected prices (2)'))
    fail = true
    await fetchPreview()
    assert.equal(container.querySelectorAll('input[type="checkbox"]').length, 0)
    assert.ok(
      container
        .querySelector('[role="alert"]')
        ?.textContent?.includes('Failed to fetch official prices')
    )
    const save = [...container.querySelectorAll('button')].find(
      (item) => item.textContent === 'Save selected prices (0)'
    )
    assert.ok(save?.disabled)
    assert.equal(requests.includes('/api/ratio_sync/fetch'), false)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.defaults.adapter = originalAdapter
  }
})

test('saving preserves unrelated edits, posts related options together, and rejects a stale selected model', async () => {
  const snapshot = Object.fromEntries(
    pricingKeys.map((key) => [key, '{}'])
  ) as Record<(typeof pricingKeys)[number], string>
  const values = { ...snapshot, ModelRatio: '{"unrelated":99}' }
  const writes: {
    before: Record<string, string>
    values: Record<string, string>
  }[] = []
  api.defaults.adapter = async (config) => {
    let data: unknown
    if (config.method === 'get' && config.url === '/api/option/') {
      data = {
        success: true,
        data: Object.entries(values).map(([key, value]) => ({ key, value })),
      }
    } else if (
      config.method === 'post' &&
      config.url === '/api/ratio_sync/official-prices/apply'
    ) {
      writes.push(JSON.parse(config.data))
      data = { success: true }
    } else {
      throw new Error('Unexpected pricing write')
    }
    return { config, data, status: 200, statusText: 'OK', headers: {} }
  }
  const selected = [
    {
      name: 'kimi-k2.6',
      price: {
        provider: 'moonshotai',
        model: 'kimi-k2.6',
        cost: { input: 10, output: 20 },
      },
    },
  ]
  try {
    await saveOfficialPrices(snapshot, selected)
    assert.equal(writes.length, 1)
    assert.deepEqual(JSON.parse(writes[0].values.ModelRatio), {
      unrelated: 99,
      'kimi-k2.6': 5,
    })
    assert.deepEqual(JSON.parse(writes[0].values.CompletionRatio), {
      'kimi-k2.6': 2,
    })
    values.ModelRatio = '{"kimi-k2.6":1}'
    await assert.rejects(
      saveOfficialPrices(snapshot, selected),
      /changed since preview/
    )
    assert.equal(writes.length, 1)
  } finally {
    api.defaults.adapter = originalAdapter
  }
})

test('list selection automatically loads only selected priced and unpriced models without selecting again', async () => {
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    const data =
      config.url === '/api/ratio_sync/official-prices'
        ? {
            success: true,
            data: [
              {
                provider: 'deepseek',
                model: 'deepseek-v4-flash',
                cost: { input: 10, output: 20 },
              },
            ],
          }
        : {
            success: true,
            data: pricingKeys.map((key) => ({
              key,
              value:
                key === 'ModelRatio'
                  ? '{"deepseek-v4-flash":1,"unselected":9}'
                  : '{}',
            })),
          }
    return { config, data, status: 200, statusText: 'OK', headers: {} }
  }
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false, gcTime: 0 },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <OfficialPriceSync
              modelNames={[
                'channel/DEEPSEEK-V4-FLASH',
                'deepseek-v4-flash',
                'unknown',
              ]}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    assert.ok(container.textContent?.includes('Confirm sync (2)'))
    assert.equal(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="Sync deepseek-v4-flash"]'
      )?.checked,
      true
    )
    assert.equal(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="Sync channel/DEEPSEEK-V4-FLASH"]'
      )?.checked,
      true
    )
    assert.equal(
      container.querySelector('input[aria-label="Sync unselected"]'),
      null
    )
    assert.equal(requests.includes('/api/channel/models_enabled'), false)
    const unknownSource = container.querySelector<HTMLInputElement>(
      '#official-source-0-2'
    )
    assert.ok(unknownSource)
    await act(async () => unknownSource.focus())
    await act(async () =>
      unknownSource.dispatchEvent(
        new browser.KeyboardEvent('keydown', {
          key: 'ArrowDown',
          bubbles: true,
        }) as unknown as Event
      )
    )
    await act(async () =>
      unknownSource.dispatchEvent(
        new browser.KeyboardEvent('keydown', {
          key: 'Enter',
          bubbles: true,
        }) as unknown as Event
      )
    )
    assert.equal(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="Sync unknown"]'
      )?.checked,
      true
    )
    assert.ok(container.textContent?.includes('Confirm sync (3)'))
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.defaults.adapter = originalAdapter
  }
})

test('pricing tabs share selection and sync both lists together from the list toolbar', async () => {
  const { RatioSettingsCard } = await import('../ratio-settings-card')
  const { SettingsPageProvider } =
    await import('../../components/settings-page-context')
  const writes: Record<string, string>[] = []
  api.defaults.adapter = async (config) => {
    let data: unknown
    if (config.url === '/api/channel/models_enabled') {
      data = { success: true, data: ['kimi-k2.6', 'deepseek-v4-flash'] }
    } else if (config.url === '/api/ratio_sync/official-prices') {
      data = {
        success: true,
        data: ['kimi-k2.6', 'deepseek-v4-flash'].map((model) => ({
          provider: model.startsWith('kimi') ? 'moonshotai' : 'deepseek',
          model,
          cost: { input: 10, output: 20 },
        })),
      }
    } else if (config.url === '/api/option/') {
      data = {
        success: true,
        data: pricingKeys.map((key) => ({
          key,
          value: key === 'ModelRatio' ? '{"deepseek-v4-flash":1}' : '{}',
        })),
      }
    } else if (config.url === '/api/ratio_sync/official-prices/apply') {
      writes.push(JSON.parse(config.data).values)
      data = { success: true }
    } else {
      throw new Error(`Unexpected request: ${config.url}`)
    }
    return { config, data, status: 200, statusText: 'OK', headers: {} }
  }
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false, gcTime: 0 },
    },
  })
  const container = document.createElement('div')
  const tabs = document.createElement('span')
  const content = document.createElement('div')
  container.append(tabs, content)
  document.body.append(container)
  const root = createRoot(content)
  const modelDefaults = {
    ModelPrice: '{}',
    ModelRatio: '{"deepseek-v4-flash":1}',
    CompletionRatio: '{}',
    CacheRatio: '{}',
    CreateCacheRatio: '{}',
    ImageRatio: '{}',
    AudioRatio: '{}',
    AudioCompletionRatio: '{}',
    ExposeRatioEnabled: false,
    BillingMode: '{}',
    BillingExpr: '{}',
  }
  const groupDefaults = {
    GroupRatio: '{}',
    TopupGroupRatio: '{}',
    UserUsableGroups: '{}',
    GroupGroupRatio: '{}',
    AutoGroups: '[]',
    MaxTokenAutoGroups: 1,
    DefaultUseAutoGroup: false,
    GroupSpecialUsableGroup: '{}',
  }
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <SettingsPageProvider
              actionsContainer={null}
              titleStatusContainer={tabs}
            >
              <RatioSettingsCard
                modelDefaults={modelDefaults}
                groupDefaults={groupDefaults}
                toolPricesDefault='{}'
                visibleTabs={['models', 'unset-models']}
              />
            </SettingsPageProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    const selectRow = () => {
      const row = container.querySelector<HTMLElement>(
        '[role="tabpanel"]:not([hidden]) [aria-label="Select row"]'
      )
      assert.ok(row)
      row.click()
    }
    await act(async () => selectRow())
    const unsetTab = [
      ...tabs.querySelectorAll<HTMLElement>('[role="tab"]'),
    ].find((tab) => tab.textContent === 'Unset price models')
    assert.ok(unsetTab)
    await act(async () => unsetTab.click())
    await act(async () => selectRow())
    const sync = [
      ...container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Sync selected prices (2)')
    assert.ok(sync)
    await act(async () => sync.click())
    const dialog = document.querySelector('[role="dialog"]')
    assert.ok(dialog)
    const confirm = [
      ...dialog.querySelectorAll<HTMLButtonElement>('button'),
    ].find((button) => button.textContent === 'Confirm sync (2)')
    assert.ok(confirm)
    await act(async () => confirm.click())
    assert.equal(writes.length, 1)
    assert.deepEqual(JSON.parse(writes[0].ModelRatio), {
      'deepseek-v4-flash': 5,
      'kimi-k2.6': 5,
    })
    assert.ok(
      [...container.querySelectorAll<HTMLButtonElement>('button')].some(
        (button) =>
          button.textContent === 'Sync selected prices (0)' && button.disabled
      )
    )
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.defaults.adapter = originalAdapter
    browser.localStorage.clear()
  }
})
