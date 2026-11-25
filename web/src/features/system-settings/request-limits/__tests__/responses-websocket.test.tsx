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

test('connection settings retain unlimited defaults, validate integers and save only on confirmation', async () => {
  await withReactTestDOM(async () => {
    const { act } = await import('react')
    const { createRoot } = await import('react-dom/client')
    const { QueryClient, QueryClientProvider } =
      await import('@tanstack/react-query')
    const { createInstance } = await import('i18next')
    const { I18nextProvider, initReactI18next } = await import('react-i18next')
    const { api } = await import('@/lib/api')
    const { SettingsPageProvider } =
      await import('../../components/settings-page-context')
    const { ResponsesWebSocketSection } =
      await import('../responses-websocket-section')

    const i18n = createInstance()
    await i18n
      .use(initReactI18next)
      .init({ lng: 'en', resources: { en: { translation: {} } } })
    const box = document.createElement('div')
    const actions = document.createElement('div')
    document.body.append(box, actions)
    const root = createRoot(box)
    const cache = new QueryClient()
    const adapter = api.defaults.adapter
    const updates: { key: string; value: string }[] = []
    api.defaults.adapter = async (config) => {
      assert.equal(config.method, 'put')
      assert.equal(config.url, '/api/option/')
      updates.push(JSON.parse(config.data as string))
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { success: true },
      }
    }

    try {
      await act(async () =>
        root.render(
          <QueryClientProvider client={cache}>
            <I18nextProvider i18n={i18n}>
              <SettingsPageProvider actionsContainer={actions}>
                <ResponsesWebSocketSection value='{"max_channel_connections":7,"future_option":23}' />
              </SettingsPageProvider>
            </I18nextProvider>
          </QueryClientProvider>
        )
      )
      const field = (labelText: string) => {
        const label = [...box.querySelectorAll('label')].find(
          (el) => el.textContent === labelText
        )
        assert.ok(label)
        const input = box.querySelector<HTMLInputElement>(
          `input[id="${label.htmlFor}"]`
        )
        assert.ok(input)
        return input
      }
      assert.equal(field('Upstream handshake').value, '45')
      assert.equal(field('Writing a message').value, '30')
      assert.equal(field('Idle between turns').value, '0')
      assert.equal(field('Upstream connections per channel').value, '7')
      const cap = field('Total client connections')
      assert.equal(cap.value, '0')
      const save = actions.querySelector('button')
      assert.ok(save)
      assert.equal(save.disabled, true)
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(setter)
      for (const value of ['1.5', '8']) {
        await act(async () => {
          setter.call(cap, value)
          cap.dispatchEvent(new Event('input', { bubbles: true }))
        })
        assert.equal(updates.length, 0)
        await act(async () => save.click())
        if (value === '1.5') {
          assert.equal(cap.getAttribute('aria-invalid'), 'true')
          assert.equal(updates.length, 0)
        }
      }
      assert.equal(updates.length, 1)
      assert.equal(updates[0].key, 'ResponsesWebSocketSettings')
      const saved = JSON.parse(updates[0].value)
      assert.equal(saved.max_connections, 8)
      assert.equal(saved.future_option, 23)
      assert.equal(saved.max_channel_connections, 7)
      assert.equal(saved.idle_seconds, 0)
      assert.equal(save.disabled, true)
    } finally {
      await act(async () => root.unmount())
      cache.clear()
      api.defaults.adapter = adapter
      box.remove()
      actions.remove()
    }
  })
})
