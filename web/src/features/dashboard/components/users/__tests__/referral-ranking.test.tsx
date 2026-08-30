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
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { ReferralRankingPage } from '@/features/dashboard/types'
import { formatQuota } from '@/lib/format'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'KeyboardEvent',
  'MouseEvent',
  'PointerEvent',
  'MutationObserver',
  'ResizeObserver',
  'IntersectionObserver',
  'DOMRect',
  'CSS',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const zhCN = (await import('@/i18n/locales/zh.json')).default
const { ReferralRankingPanel } = await import('../referral-ranking')
const { getLocalTodayRange, getMillisecondsUntilNextLocalDay } =
  await import('../referral-ranking-time')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  fallbackLng: false,
  lng: 'zhCN',
  nsSeparator: false,
  resources: { zhCN },
  supportedLngs: ['zhCN'],
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type MountedComponent = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

const mountedComponents = new Set<MountedComponent>()

async function renderComponent(element: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const mounted = { container, root }
  mountedComponents.add(mounted)

  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{element}</I18nextProvider>)
  })

  return container
}

const rankingPage: ReferralRankingPage = {
  page: 2,
  page_size: 10,
  total: 26,
  items: [
    {
      user_id: 101,
      username: 'alice',
      display_name: 'Alice Zhang',
      invited_count: 18,
      qualified_count: 12,
      topup_quota: 7_500_000,
      commission_quota: 375_000,
      commission_count: 9,
    },
    {
      user_id: 102,
      username: 'bob',
      invited_count: 11,
      qualified_count: 8,
      topup_quota: 5_000_000,
      commission_quota: 250_000,
      commission_count: 6,
    },
  ],
}

describe('admin referral ranking', () => {
  afterEach(async () => {
    for (const mounted of mountedComponents) {
      await act(async () => mounted.root.unmount())
      mounted.container.remove()
    }
    mountedComponents.clear()
  })

  after(() => {
    domWindow.close()
  })

  test('builds a half-open range using the browser local calendar day', () => {
    const now = new Date(2026, 7, 30, 14, 25, 42, 500)
    const range = getLocalTodayRange(now)
    assert.deepEqual(range, {
      startTimestamp: Math.floor(new Date(2026, 7, 30).getTime() / 1000),
      endTimestamp: Math.floor(new Date(2026, 7, 31).getTime() / 1000),
    })
    assert.equal(
      getMillisecondsUntilNextLocalDay(now),
      new Date(2026, 7, 31).getTime() - now.getTime() + 50
    )
  })

  test('renders the all-time ranking metrics, continuous rank, and pagination', async () => {
    const changedPages: number[] = []
    const openedUsers: number[] = []
    const container = await renderComponent(
      <ReferralRankingPanel
        data={rankingPage}
        error={false}
        fetching={false}
        loading={false}
        onPageChange={(page) => changedPages.push(page)}
        onPageSizeChange={() => {}}
        onPeriodChange={() => {}}
        onRetry={() => {}}
        onUserOpen={(userId) => openedUsers.push(userId)}
        page={2}
        pageSize={10}
        period='all'
      />
    )

    const headings = [
      ...container.querySelectorAll('[data-referral-ranking] thead th'),
    ].map((element) => element.textContent?.trim())
    assert.deepEqual(headings, [
      '排名',
      '邀请人',
      '邀请总数',
      '真实用户',
      '总充值金额',
      '累计分成',
    ])
    assert.equal(container.textContent?.includes('Alice Zhang'), true)
    assert.equal(container.textContent?.includes('@alice · #101'), true)
    assert.equal(container.textContent?.includes(formatQuota(7_500_000)), true)
    assert.equal(container.textContent?.includes('9 笔分成'), true)

    const desktopRows = container.querySelectorAll('tbody tr')
    assert.equal(desktopRows[0]?.querySelector('td')?.textContent?.trim(), '11')
    assert.equal(desktopRows[1]?.querySelector('td')?.textContent?.trim(), '12')

    const aliceButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Alice Zhang'
    )
    assert.ok(aliceButton)
    await act(async () => aliceButton.click())
    assert.deepEqual(openedUsers, [101])

    const nextPageButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.includes('前往下一页')
    )
    assert.ok(nextPageButton)
    await act(async () => nextPageButton.click())
    assert.deepEqual(changedPages, [3])
  })

  test('uses period-specific labels for today and reports period changes', async () => {
    const changedPeriods: string[] = []
    const container = await renderComponent(
      <ReferralRankingPanel
        data={{ ...rankingPage, page: 1 }}
        error={false}
        fetching={false}
        loading={false}
        onPageChange={() => {}}
        onPageSizeChange={() => {}}
        onPeriodChange={(period) => changedPeriods.push(period)}
        onRetry={() => {}}
        onUserOpen={() => {}}
        page={1}
        pageSize={10}
        period='today'
      />
    )

    const headings = [
      ...container.querySelectorAll('[data-referral-ranking] thead th'),
    ].map((element) => element.textContent?.trim())
    assert.deepEqual(headings.slice(2), [
      '今日邀请',
      '今日真实用户',
      '今日充值金额',
      '今日分成',
    ])
    assert.equal(
      container.textContent?.includes(
        '今日排行统计你所在时区自然日内新邀请的用户。'
      ),
      true
    )

    const allTimeButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === '全部时间'
    )
    assert.ok(allTimeButton)
    await act(async () => allTimeButton.click())
    assert.deepEqual(changedPeriods, ['all'])
  })
})
