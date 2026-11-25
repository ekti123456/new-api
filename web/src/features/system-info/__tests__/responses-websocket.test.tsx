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

import { test } from 'vitest'

import { withReactTestDOM } from '@/test-support/react-dom'

test('system info shows the serving node, both connection legs, release reasons and refresh errors', async () => {
  await withReactTestDOM(async () => {
    const { act } = await import('react')
    const { createRoot } = await import('react-dom/client')
    const { QueryClient, QueryClientProvider, notifyManager } =
      await import('@tanstack/react-query')
    const { createInstance } = await import('i18next')
    const { I18nextProvider, initReactI18next } = await import('react-i18next')
    const { api } = await import('@/lib/api')
    const { ResponsesWebSocketPanel } =
      await import('../components/responses-websocket-panel')

    const i18n = createInstance()
    await i18n
      .use(initReactI18next)
      .init({ lng: 'en', resources: { en: { translation: {} } } })
    const box = document.createElement('div')
    document.body.append(box)
    const root = createRoot(box)
    const cache = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const adapter = api.defaults.adapter
    const snapshot = {
      node: 'node-a',
      started_at: 120,
      connections: {
        current: 4,
        max_connections: 12,
        max_client_handshakes: 0,
        max_handshakes: 0,
        peak: 7,
        client_handshakes: 0,
        waiting_first: 1,
        active: 1,
        idle: 2,
        closing: 0,
        upstream_handshakes: 1,
        upstream_connections: 2,
        opened: 9,
        released: 5,
        dial_attempts: 7,
        dial_errors: 0,
        dial_canceled: 2,
        draining: false,
        rejections: { user: 2 },
        release_reasons: { client_closed: 5 },
      },
    }
    let fail = false
    api.defaults.adapter = async (config) => {
      assert.equal(config.url, '/api/system-info/responses-websocket')
      const data = fail
        ? { success: false, message: 'unavailable' }
        : { success: true, data: snapshot }
      return { config, status: 200, statusText: 'OK', headers: {}, data }
    }

    try {
      const key = ['system-info', 'responses-websocket']
      const flushQuery = () =>
        act(async () => {
          await cache
            .getQueryCache()
            .find({ queryKey: key })
            ?.promise?.catch(() => undefined)
          await new Promise<void>((resolve) => notifyManager.schedule(resolve))
        })
      await act(async () =>
        root.render(
          <QueryClientProvider client={cache}>
            <I18nextProvider i18n={i18n}>
              <ResponsesWebSocketPanel />
            </I18nextProvider>
          </QueryClientProvider>
        )
      )
      await flushQuery()
      assert.match(box.textContent ?? '', /node-a/)
      const count = (label: string) =>
        [...box.querySelectorAll('dt')].find((el) => el.textContent === label)
          ?.nextElementSibling?.textContent
      assert.equal(count('Client connections'), '4 / 12')
      assert.equal(count('Connected upstreams'), '2')
      assert.equal(count('Released connections'), '5')
      assert.equal(count('Client disconnected'), '5')
      assert.equal(count('User limit'), '2')
      const refresh = box.querySelector('button')
      assert.ok(refresh)
      fail = true
      await act(async () => refresh.click())
      await flushQuery()
      assert.ok(box.querySelector('[role="alert"]'))
      assert.equal(cache.getQueryState(key)?.status, 'error')
      assert.equal(count('Client connections'), '4 / 12')
    } finally {
      await act(async () => root.unmount())
      cache.clear()
      api.defaults.adapter = adapter
      box.remove()
    }
  })
})
