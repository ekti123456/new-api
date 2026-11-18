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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { FastRuleSyncPanel } from '../fast-rule-sync-panel'
import { pricingKeys } from '../official-price-matching'

const originalAdapter = api.defaults.adapter
let client: QueryClient
let expression: string
let calls: string[]
let writes: Array<{
  before: Record<string, string>
  values: Record<string, string>
}>

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false, gcTime: 0 },
    },
  })
  expression = 'tier("base", p * 2 + c * 10)'
  calls = []
  writes = []
  api.defaults.adapter = async (config) => {
    calls.push(config.url ?? '')
    let data: unknown
    if (config.url === '/api/option/') {
      const values: Record<string, string> = {
        'billing_setting.billing_mode': '{"gpt-6.2-sol":"tiered_expr"}',
        'billing_setting.billing_expr': JSON.stringify({
          'gpt-6.2-sol': expression,
          untouched: 'p * 99',
        }),
      }
      data = {
        success: true,
        data: pricingKeys.map((key) => ({ key, value: values[key] ?? '{}' })),
      }
    } else if (config.url === '/api/ratio_sync/official-prices/apply') {
      writes.push(JSON.parse(config.data))
      data = { success: true }
    } else throw new Error(`Unexpected request: ${config.url}`)
    return { config, data, status: 200, statusText: 'OK', headers: {} }
  }
})

afterEach(() => {
  api.defaults.adapter = originalAdapter
  client.clear()
})

it('previews selected Fast rules and saves once without fetching models.dev or rewriting base prices', async () => {
  const saved = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <FastRuleSyncPanel
        modelNames={['gpt-6.2-sol', 'claude-opus-5']}
        onSaved={saved}
        onSavingChange={() => {}}
      />
    </QueryClientProvider>
  )
  expect(
    screen.getByRole('button', { name: 'Sync Fast rules (0)' })
  ).toBeDisabled()
  expect(await screen.findByText('Ready to sync')).toBeInTheDocument()
  expect(
    screen.getByText('Skipped: not an OpenAI text model')
  ).toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', { name: 'Sync Fast rules (1)' })
  )
  await waitFor(() => expect(saved).toHaveBeenCalledWith(['gpt-6.2-sol']))
  expect(writes).toHaveLength(1)
  expect(Object.keys(writes[0].values)).toEqual([
    'billing_setting.billing_expr',
  ])
  const values = JSON.parse(writes[0].values['billing_setting.billing_expr'])
  expect(values.untouched).toBe('p * 99')
  expect(values['gpt-6.2-sol']).toContain('(tier("base", p * 2 + c * 10))')
  expect(values['gpt-6.2-sol']).toContain('"ultrafast" ? 6 : 1')
  expect(calls).toEqual([
    '/api/option/',
    '/api/option/',
    '/api/ratio_sync/official-prices/apply',
  ])
})

it('refuses stale selected prices and requires a refreshed preview before retrying', async () => {
  const saved = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <FastRuleSyncPanel
        modelNames={['gpt-6.2-sol']}
        onSaved={saved}
        onSavingChange={() => {}}
      />
    </QueryClientProvider>
  )
  await screen.findByText('Ready to sync')
  expression = 'tier("base", p * 5 + c * 25)'
  await userEvent.click(
    screen.getByRole('button', { name: 'Sync Fast rules (1)' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Pricing changed since preview'
  )
  expect(writes).toEqual([])
  expect(saved).not.toHaveBeenCalled()
  expect(
    screen.getByRole('button', { name: 'Sync Fast rules (1)' })
  ).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Sync Fast rules (1)' })
    ).toBeEnabled()
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Sync Fast rules (1)' })
  )
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0].values['billing_setting.billing_expr']).toContain(
    'p * 5 + c * 25'
  )
})
