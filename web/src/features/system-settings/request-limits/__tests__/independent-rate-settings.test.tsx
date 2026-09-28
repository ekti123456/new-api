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

import {
  QueryClient,
  QueryClientProvider,
  notifyManager,
} from '@tanstack/react-query'
import { createInstance } from 'i18next'
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterAll, test } from 'vitest'

import { api } from '@/lib/api'

import { independentRateSchema, type RateEditor } from '../independent-rate-api'
import { IndependentRateSection } from '../independent-rate-section'

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
notifyManager.setScheduler((callback) => callback())
afterAll(() =>
  notifyManager.setScheduler((callback) => setTimeout(callback, 0))
)

const initial: RateEditor = {
  revision: 'first',
  settings: {
    enabled: false,
    namespace: 'review',
    rules: [
      {
        id: 'chat',
        name: 'Go non-stream chat',
        enabled: true,
        ua_mode: 'exact',
        ua: 'Go-http-client/2.0',
        path: '/v1/chat/completions',
        stream: 'non_stream',
        limit: 100,
        overrides: [{ user_id: 42, limit: 200 }],
      },
    ],
  },
}

test('independent settings save separately and show user request counts', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const query = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  query.setQueryData(['independent-rpm-settings'], structuredClone(initial))
  const adapter = api.defaults.adapter
  const writes: RateEditor[] = []
  const usageCalls: string[] = []
  api.defaults.adapter = async (config) => {
    let data: unknown = initial
    if (config.url?.endsWith('/usage')) {
      usageCalls.push(String(config.params.rule_id))
      data = {
        items: [
          {
            user_id: 42,
            username: 'alice',
            display_name: '',
            count: 40,
            limit: 200,
            allowed: true,
            retry_after: 0,
          },
        ],
        total: 1,
        page: 1,
        size: 20,
        backend: 'redis',
        enabled: true,
      }
    }
    if (config.method === 'put') {
      assert.equal(config.url, '/api/option/request-rate-limits')
      const editor = JSON.parse(config.data as string) as RateEditor
      writes.push(editor)
      data = { ...editor, revision: 'saved' }
    }
    return {
      data: { success: true, data },
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
            <IndependentRateSection />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    const toggle = container.querySelector<HTMLElement>('[role="switch"]')
    assert.ok(toggle)
    await act(async () => toggle.click())
    assert.equal(writes.length, 0, 'editing must not apply the live limit')
    const save = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Save independent limits'
    )
    assert.ok(save)
    await act(async () => save.click())
    assert.equal(writes.length, 1)
    assert.equal(writes[0].settings.enabled, true)
    assert.equal(writes[0].settings.rules[0].stream, 'non_stream')
    assert.equal(writes[0].settings.rules[0].overrides[0].limit, 200)
    const usage = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Usage'
    )
    assert.ok(usage)
    await act(async () => usage.click())
    assert.match(container.textContent ?? '', /40 \/ 200/)
    assert.match(container.textContent ?? '', /not concurrent requests/)
    assert.ok(usageCalls.every((id) => id === 'chat'))
    assert.equal(writes.length, 1, 'live usage reads cannot save configuration')
  } finally {
    await act(async () => root.unmount())
    query.clear()
    api.defaults.adapter = adapter
    container.remove()
  }
})

test('rule validation rejects broad paths and invalid RPM while preserving non-stream matching', () => {
  assert.equal(independentRateSchema.safeParse(initial).success, true)
  const invalid = structuredClone(initial)
  invalid.settings.rules[0].path = '/v1/*'
  assert.equal(independentRateSchema.safeParse(invalid).success, false)
  invalid.settings.rules[0].path = '/v1/chat/completions'
  invalid.settings.rules[0].limit = 0
  assert.equal(independentRateSchema.safeParse(invalid).success, false)
  invalid.settings.rules[0].limit = 100
  invalid.settings.rules[0].ua = ''
  assert.equal(independentRateSchema.safeParse(invalid).success, false)
})
