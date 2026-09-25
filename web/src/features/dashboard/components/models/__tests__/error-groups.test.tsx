import assert from 'node:assert/strict'
import { after, test, type TestContext } from 'node:test'

import { Window } from 'happy-dom'

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
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
  fallbackLng: 'en',
})
const { api } = await import('@/lib/api')
const { PerformanceErrorsPanel } = await import('../performance-errors-panel')
after(() => browser.close())

const latest = {
  id: 3,
  user_id: 7,
  username: 'alice',
  created_at: 1789200000,
  model_name: 'gpt-a',
  group: 'pro',
  status_code: 429,
  error_type: 'admission',
  error_code: 'concurrency',
  error_reason: 'busy',
  request_id: 'latest-request',
  group_key: 'alice-concurrency',
  error_group_id: 3,
  occurrence_count: 2,
}
const older = { ...latest, id: 1, request_id: 'older-request' }

async function mountPanel(
  context: TestContext,
  response: (params: Record<string, unknown>, method?: string) => unknown
) {
  const originalAdapter = api.defaults.adapter
  const requests: Record<string, unknown>[] = []
  api.defaults.adapter = async (config) => {
    const params = (config.params ?? {}) as Record<string, unknown>
    requests.push(params)
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: await response(params, config.method),
    }
  }
  notifyManager.setScheduler((callback) => callback())
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  context.after(async () => {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.defaults.adapter = originalAdapter
    notifyManager.setScheduler((callback) => setTimeout(callback, 0))
  })
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <PerformanceErrorsPanel
          filters={{
            start_timestamp: new Date(1789100000000),
            end_timestamp: new Date(1789300000000),
          }}
        />
      </QueryClientProvider>
    )
  })
  return { container, requests, client }
}

test('clearing performance errors requires confirmation and refreshes groups after success', async (context) => {
  let cleared = false
  let deletions = 0
  const { container } = await mountPanel(context, (_params, method) => {
    if (method === 'delete') {
      deletions++
      cleared = true
      return { success: true, data: { deleted: 2 } }
    }
    return {
      success: true,
      data: {
        page: 1,
        page_size: 20,
        total: cleared ? 0 : 1,
        total_occurrences: cleared ? 0 : 2,
        items: cleared ? [] : [latest],
      },
    }
  })
  const clear = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Clear performance errors"]'
  )
  assert.ok(clear)
  await act(async () => clear.click())
  assert.equal(deletions, 0)
  const dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(dialog)
  assert.match(
    dialog.textContent || '',
    /Usage logs, billing and performance statistics are preserved/
  )
  const cancel = [...dialog.querySelectorAll('button')].find(
    (button) => button.textContent === 'Cancel'
  )
  assert.ok(cancel)
  await act(async () => cancel.click())
  assert.equal(deletions, 0)
  await act(async () => clear.click())
  const confirm = [
    ...document.querySelectorAll<HTMLButtonElement>(
      '[role="alertdialog"] button'
    ),
  ].find((button) => button.textContent === 'Clear all performance errors')
  assert.ok(confirm)
  await act(async () => confirm.click())
  assert.equal(deletions, 1)
  assert.match(
    container.textContent || '',
    /No performance errors in the selected period/
  )
  assert.equal(container.textContent?.includes('latest-request'), false)
})

test('a failed cleanup keeps existing errors and allows retry', async (context) => {
  const { container } = await mountPanel(context, (_params, method) =>
    method === 'delete'
      ? { success: false, message: 'database unavailable' }
      : {
          success: true,
          data: {
            page: 1,
            page_size: 20,
            total: 1,
            total_occurrences: 2,
            items: [latest],
          },
        }
  )
  const clear = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Clear performance errors"]'
  )
  assert.ok(clear)
  await act(async () => clear.click())
  const confirm = [
    ...document.querySelectorAll<HTMLButtonElement>(
      '[role="alertdialog"] button'
    ),
  ].find((button) => button.textContent === 'Clear all performance errors')
  assert.ok(confirm)
  await act(async () => confirm.click())
  assert.match(
    document.body.textContent || '',
    /Unable to clear performance errors/
  )
  assert.match(container.textContent || '', /latest-request/)
  assert.equal(confirm.disabled, false)
})

test('cleanup in progress disables confirmation and prevents duplicate submissions', async (context) => {
  let finish: (value: unknown) => void = () => {}
  const pending = new Promise((resolve) => {
    finish = resolve
  })
  let deletions = 0
  const { container } = await mountPanel(context, (_params, method) => {
    if (method === 'delete') {
      deletions++
      return pending
    }
    return {
      success: true,
      data: {
        page: 1,
        page_size: 20,
        total: 0,
        total_occurrences: 0,
        items: [],
      },
    }
  })
  const clear = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Clear performance errors"]'
  )
  assert.ok(clear)
  await act(async () => clear.click())
  const confirm = [
    ...document.querySelectorAll<HTMLButtonElement>(
      '[role="alertdialog"] button'
    ),
  ].find((button) => button.textContent === 'Clear all performance errors')
  assert.ok(confirm)
  await act(async () => confirm.click())
  assert.equal(confirm.disabled, true)
  assert.equal(clear.disabled, true)
  await act(async () => confirm.click())
  assert.equal(deletions, 1)
  await act(async () => finish({ success: true, data: { deleted: 0 } }))
  assert.equal(clear.disabled, false)
})

test('same-user error groups start folded and clicking the count loads and collapses original requests', async (context) => {
  const { container, requests } = await mountPanel(context, (params) => ({
    success: true,
    data: {
      page: 1,
      page_size: 20,
      total: params.error_group_id ? 2 : 1,
      total_occurrences: 2,
      items: params.error_group_id ? [latest, older] : [latest],
    },
  }))
  assert.equal(requests[0].grouped, true)
  const toggle = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Expand 2 errors"]'
  )
  assert.ok(toggle)
  assert.equal(toggle.textContent?.trim(), '2')
  assert.equal(toggle.getAttribute('aria-expanded'), 'false')
  assert.equal(container.textContent?.includes('older-request'), false)
  assert.equal(
    requests.some((request) => request.error_group_id),
    false
  )
  await act(async () => toggle.click())
  assert.equal(toggle.getAttribute('aria-expanded'), 'true')
  assert.match(container.textContent || '', /older-request/)
  const details = requests.find((request) => request.error_group_id)
  assert.equal(details?.error_group_id, 3)
  assert.equal(details?.grouped, false)
  assert.equal(details?.start_timestamp, 1789100000)
  assert.equal(details?.end_timestamp, 1789300000)
  await act(async () => toggle.click())
  assert.equal(toggle.getAttribute('aria-expanded'), 'false')
  assert.equal(container.textContent?.includes('older-request'), false)
})

test('failed group expansion keeps the summary and supports retry without displaying unrelated logs', async (context) => {
  let failDetails = true
  const { container } = await mountPanel(context, (params) => {
    if (params.error_group_id && failDetails) {
      return { success: false, message: 'unavailable' }
    }
    return {
      success: true,
      data: {
        page: 1,
        page_size: 20,
        total: 1,
        items: params.error_group_id ? [older] : [latest],
      },
    }
  })
  const toggle = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Expand 2 errors"]'
  )
  assert.ok(toggle)
  await act(async () => toggle.click())
  assert.match(
    container.querySelector('[role=alert]')?.textContent || '',
    /Unable to load performance errors/
  )
  assert.match(container.textContent || '', /latest-request/)
  failDetails = false
  const retry = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((button) => button.textContent === 'Retry')
  assert.ok(retry)
  await act(async () => retry.click())
  assert.equal(container.querySelector('[role=alert]'), null)
  assert.match(container.textContent || '', /older-request/)
})

test('empty grouped results display the empty state instead of an expansion control', async (context) => {
  const { container } = await mountPanel(context, () => ({
    success: true,
    data: { page: 1, page_size: 20, total: 0, total_occurrences: 0, items: [] },
  }))
  assert.match(
    container.textContent || '',
    /No performance errors in the selected period/
  )
  assert.equal(container.querySelector('button[aria-label^="Expand "]'), null)
})

test('expanded groups paginate independently and reopening starts on the first detail page', async (context) => {
  const { container, requests } = await mountPanel(context, (params) => ({
    success: true,
    data: {
      page: params.p,
      page_size: 20,
      total: params.error_group_id ? 21 : 1,
      total_occurrences: 21,
      items: params.error_group_id
        ? [params.p === 2 ? older : latest]
        : [{ ...latest, occurrence_count: 21 }],
    },
  }))
  const toggle = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Expand 21 errors"]'
  )
  assert.ok(toggle)
  toggle.focus()
  assert.equal(document.activeElement, toggle)
  assert.equal(toggle.type, 'button')
  await act(async () => toggle.click())
  const region = container.querySelector('[role=region]')
  assert.ok(region)
  const next = [...region.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent === 'Next page'
  )
  assert.ok(next)
  await act(async () => next.click())
  assert.match(region.textContent || '', /older-request/)
  assert.equal(requests.at(-1)?.p, 2)
  assert.equal(requests.at(-1)?.error_group_id, 3)
  const lastPageNext = [
    ...region.querySelectorAll<HTMLButtonElement>('button'),
  ].find((button) => button.textContent === 'Next page')
  assert.ok(lastPageNext)
  assert.equal(lastPageNext.disabled, true)
  await act(async () => toggle.click())
  await act(async () => toggle.click())
  assert.match(
    container.querySelector('[role=region]')?.textContent || '',
    /Page 1 of 2/
  )
})

test('refreshing the same group updates its count without collapsing the open details', async (context) => {
  let refreshed = false
  const { container, client } = await mountPanel(context, (params) => ({
    success: true,
    data: {
      page: 1,
      page_size: 20,
      total: params.error_group_id ? 2 : 1,
      items: params.error_group_id
        ? [older]
        : [
            {
              ...latest,
              error_group_id: refreshed ? 4 : 3,
              occurrence_count: refreshed ? 3 : 2,
            },
          ],
    },
  }))
  const toggle = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Expand 2 errors"]'
  )
  assert.ok(toggle)
  await act(async () => toggle.click())
  refreshed = true
  await act(async () =>
    client.invalidateQueries({ queryKey: ['dashboard-performance-errors'] })
  )
  assert.equal(toggle.getAttribute('aria-expanded'), 'true')
  assert.equal(toggle.textContent?.trim(), '3')
  assert.match(
    container.querySelector('[role=region]')?.textContent || '',
    /older-request/
  )
})

test('single errors expand with wrapping details and a missing group identifier never loads all requests', async (context) => {
  const reason =
    'Upstream closed the connection after accepting the request; completion cannot be confirmed.'
  const { container, requests } = await mountPanel(context, (params) => ({
    success: true,
    data: {
      page: 1,
      page_size: 20,
      total: 1,
      items: [
        { ...latest, occurrence_count: 1, error_reason: reason },
        ...(!params.error_group_id
          ? [
              {
                ...older,
                group_key: 'legacy',
                error_group_id: undefined,
                occurrence_count: 1,
              },
            ]
          : []),
      ],
    },
  }))
  const toggles = [
    ...container.querySelectorAll<HTMLButtonElement>(
      'button[aria-label="Expand 1 errors"]'
    ),
  ]
  assert.equal(toggles.length, 2)
  assert.equal(toggles[1].disabled, true)
  await act(async () => toggles[1].click())
  assert.equal(
    requests.some((request) => request.error_group_id),
    false
  )
  await act(async () => toggles[0].click())
  const detailReason = container
    .querySelector('[role=region]')
    ?.querySelector(`span[title="${reason}"]`)
  assert.ok(detailReason)
  assert.equal(detailReason.textContent, reason)
  assert.equal(detailReason.classList.contains('whitespace-pre-wrap'), true)
})
