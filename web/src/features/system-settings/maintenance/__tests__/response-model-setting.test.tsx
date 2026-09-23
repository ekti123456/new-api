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

const domWindow = new Window()
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
const previousGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of globals) {
  previousGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
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
const { LogSettingsSection } = await import('../log-settings-section')

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const reactGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
const previousAct = reactGlobals.IS_REACT_ACT_ENVIRONMENT
reactGlobals.IS_REACT_ACT_ENVIRONMENT = true
after(() => {
  domWindow.close()
  for (const key of globals) {
    const descriptor = previousGlobals.get(key)
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
  reactGlobals.IS_REACT_ACT_ENVIRONMENT = previousAct
})

test('response model logging defaults off and only saves when the administrator clicks Save', async () => {
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
    if (config.method === 'put') updates.push(JSON.parse(config.data as string))
    return {
      data: { success: true, data: null },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  try {
    const render = async (enabled?: boolean) =>
      act(async () =>
        root.render(
          <QueryClientProvider client={queryClient}>
            <I18nextProvider i18n={i18n}>
              <SettingsPageProvider actionsContainer={actions}>
                <LogSettingsSection
                  defaultEnabled
                  defaultIPLogEnabled
                  defaultResponseModelLogEnabled={enabled}
                />
              </SettingsPageProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
      )
    await render()
    const toggle = container.querySelector<HTMLElement>(
      '[role="switch"][aria-label="Record and show upstream response model"]'
    )
    assert.ok(toggle)
    assert.equal(toggle.getAttribute('aria-checked'), 'false')
    await act(async () => toggle.click())
    assert.equal(toggle.getAttribute('aria-checked'), 'true')
    assert.deepEqual(updates, [])
    const save = [...actions.querySelectorAll('button')].find(
      (item) => item.textContent === 'Save log settings'
    )
    assert.ok(save)
    await act(async () => save.click())
    assert.deepEqual(updates, [
      { key: 'UpstreamResponseModelLogEnabled', value: true },
    ])
    await render(true)
    await act(async () => toggle.click())
    assert.equal(toggle.getAttribute('aria-checked'), 'false')
    assert.equal(updates.length, 1)
    await act(async () => save.click())
    assert.deepEqual(updates[1], {
      key: 'UpstreamResponseModelLogEnabled',
      value: false,
    })
  } finally {
    await act(async () => root.unmount())
    api.defaults.adapter = originalAdapter
    queryClient.clear()
    container.remove()
    actions.remove()
  }
})
