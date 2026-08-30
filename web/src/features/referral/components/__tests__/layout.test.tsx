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

import type {
  ReferralCommission,
  ReferralCommissionFilters,
  ReferralMember,
  ReferralMemberFilters,
  ReferralSummary,
} from '@/features/wallet/types'
import { formatQuota } from '@/lib/format'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
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
const zhTW = (await import('@/i18n/locales/zh-TW.json')).default
const { ReferralOverview } = await import('../referral-overview')
const { ReferralCommissionDetails } =
  await import('../referral-commission-details')
const { ReferralMembers } = await import('../referral-members')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  fallbackLng: false,
  lng: 'zhCN',
  nsSeparator: false,
  resources: { zhCN, zhTW },
  supportedLngs: ['zhCN', 'zhTW'],
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

function textValues(elements: NodeListOf<Element>): string[] {
  return [...elements].map((element) => element.textContent?.trim() ?? '')
}

function setControlledInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

const summary: ReferralSummary = {
  aff_code: 'invite-code',
  invited_count: 9,
  qualified_count: 6,
  rate_bps: 600,
  users_per_tier: 5,
  next_tier_remaining: 4,
  max_rate_bps: 1000,
  qualified_topup_quota: 500000,
  frozen_quota: 25000,
  available_quota: 75000,
  history_quota: 180000,
  referred_topup_quota: 3600000,
  commission_count: 12,
}

const members: ReferralMember[] = [
  {
    id: 81726354,
    username: 'legacy-alice',
    created_at: 1_725_000_000,
    referral_topup_quota: 750_000,
    referral_qualified_at: 1_725_100_000,
    legacy: true,
  },
  {
    id: 91827364,
    username: 'new-bob',
    created_at: 1_726_000_000,
    referral_topup_quota: 200_000,
    referral_qualified_at: 0,
    legacy: false,
  },
]

describe('referral page layout', () => {
  afterEach(async () => {
    for (const mounted of mountedComponents) {
      await act(async () => mounted.root.unmount())
      mounted.container.remove()
    }
    mountedComponents.clear()
    await i18n.changeLanguage('zhCN')
  })

  after(() => {
    domWindow.close()
  })

  test('renders the referral link, five summary cards, reward balance, and four rules in Simplified Chinese', async () => {
    const affiliateLink = 'https://example.com/register?aff=invite-code'
    const container = await renderComponent(
      <ReferralOverview
        affiliateLink={affiliateLink}
        loading={false}
        onTransfer={() => undefined}
        summary={summary}
      />
    )

    const linkInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="你的邀请链接"]'
    )
    assert.ok(linkInput)
    assert.equal(linkInput.value, affiliateLink)

    const statGrid = container.querySelector('[data-referral-stat-grid]')
    assert.ok(statGrid)
    assert.equal(statGrid.children.length, 5)
    const statText = statGrid.textContent ?? ''
    for (const label of [
      '累计分成',
      '分成比例',
      '成员累计到账额度',
      '邀请人数',
      '分成次数',
    ]) {
      assert.equal(
        statText.includes(label),
        true,
        `missing statistic: ${label}`
      )
    }
    const invitedMembersCard = [...statGrid.children].find((card) =>
      card.textContent?.includes('邀请人数')
    )
    assert.ok(invitedMembersCard)
    assert.equal(invitedMembersCard.textContent?.includes('6'), true)
    assert.equal(invitedMembersCard.textContent?.includes('9'), false)

    const headings = textValues(container.querySelectorAll('h3'))
    assert.equal(headings.includes('奖励余额'), true)
    const rewardCard = [
      ...container.querySelectorAll('[data-slot="card"]'),
    ].find((card) => card.querySelector('h3')?.textContent === '奖励余额')
    assert.ok(rewardCard)
    assert.equal(statGrid.contains(rewardCard), false)

    for (const title of ['计算基准', '冻结划转', '有效来源', '统计范围']) {
      assert.equal(
        container.textContent?.includes(title),
        true,
        `missing rule: ${title}`
      )
    }

    assert.equal(
      /\b(?:Member|Reward)\b/.test(container.textContent ?? ''),
      false
    )
  })

  test('keeps the empty commission table headings and filters visible in Simplified Chinese', async () => {
    const searchCalls: ReferralCommissionFilters[] = []
    const container = await renderComponent(
      <ReferralCommissionDetails
        loading={false}
        onPageChange={() => undefined}
        onPageSizeChange={() => undefined}
        onSearch={(filters) => searchCalls.push(filters)}
        page={1}
        pageSize={10}
        paymentMethods={[{ name: '自定义支付', type: 'custom1' }]}
        records={[]}
        total={0}
      />
    )

    const headers = textValues(container.querySelectorAll('thead th'))
    assert.deepEqual(headers, [
      '成员',
      '来源',
      '充值额度',
      '分成 · 比例',
      '时间',
      '状态',
    ])
    assert.equal(container.textContent?.includes('暂无分成记录'), true)
    assert.equal(
      container.textContent?.includes(
        '历史分成不补发；成员下一笔成功在线充值后，新的分成记录会显示在这里。'
      ),
      true
    )

    assert.ok(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="搜索成员名称"]'
      )
    )
    const sourceTrigger = [
      ...container.querySelectorAll<HTMLButtonElement>(
        '[data-slot="select-trigger"]'
      ),
    ].find((trigger) => trigger.textContent?.includes('全部来源'))
    assert.ok(sourceTrigger)
    await act(async () => sourceTrigger.click())
    const customPaymentOption = [
      ...document.querySelectorAll<HTMLElement>('[data-slot="select-item"]'),
    ].find((item) => item.textContent?.includes('自定义支付'))
    assert.ok(customPaymentOption)
    await act(async () => customPaymentOption.click())

    const visibleButtonNames = textValues(container.querySelectorAll('button'))
    assert.equal(visibleButtonNames.includes('时间范围'), true)
    assert.equal(visibleButtonNames.includes('重置'), true)
    assert.equal(visibleButtonNames.includes('搜索'), true)
    assert.equal(
      /\b(?:Member|Reward)\b/.test(container.textContent ?? ''),
      false
    )

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索成员名称"]'
    )
    const searchButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === '搜索'
    )
    assert.ok(searchInput)
    assert.ok(searchButton)
    await act(async () => {
      setControlledInputValue(searchInput, 'alice')
    })
    await act(async () => searchButton.click())
    assert.equal(searchCalls.at(-1)?.keyword, '%alice%')
    assert.equal(searchCalls.at(-1)?.payment_method, 'custom1')

    await act(async () => {
      setControlledInputValue(searchInput, 'a')
    })
    await act(async () => searchButton.click())
    assert.equal(searchCalls.at(-1)?.keyword, 'a')
  })

  test('shows concrete historical and new invited members without exposing numeric IDs', async () => {
    const searchCalls: ReferralMemberFilters[] = []
    const container = await renderComponent(
      <ReferralMembers
        error={false}
        loading={false}
        members={members}
        onPageChange={() => undefined}
        onPageSizeChange={() => undefined}
        onRetry={() => undefined}
        onSearch={(filters) => searchCalls.push(filters)}
        page={1}
        pageSize={10}
        qualifiedTopupQuota={500_000}
        total={members.length}
      />
    )

    assert.deepEqual(textValues(container.querySelectorAll('thead th')), [
      '成员',
      '邀请类型',
      '累计在线充值',
      '资格状态',
      '邀请时间',
    ])
    assert.equal(
      container.querySelector('[data-slot="card-title"]')?.textContent,
      '邀请成员'
    )

    const rows = [...container.querySelectorAll('tbody tr')]
    const legacyRow = rows.find((row) =>
      row.textContent?.includes('legacy-alice')
    )
    const newRow = rows.find((row) => row.textContent?.includes('new-bob'))
    assert.ok(legacyRow)
    assert.ok(newRow)
    assert.equal(legacyRow.textContent?.includes('旧邀请'), true)
    assert.equal(legacyRow.textContent?.includes('已达标'), true)
    assert.equal(legacyRow.textContent?.includes(formatQuota(750_000)), true)
    assert.equal(newRow.textContent?.includes('新邀请'), true)
    assert.equal(newRow.textContent?.includes('待达标'), true)
    assert.equal(container.textContent?.includes('81726354'), false)
    assert.equal(container.textContent?.includes('91827364'), false)
    assert.equal(
      container.textContent?.includes(
        '旧邀请和新邀请都会显示。历史成功在线充值计入真实用户资格，但不补发历史分成；成员下一笔成功在线充值起正常返佣。'
      ),
      true
    )

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索邀请成员"]'
    )
    const searchButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === '搜索'
    )
    assert.ok(searchInput)
    assert.ok(searchButton)
    await act(async () => {
      setControlledInputValue(searchInput, 'alice')
    })
    await act(async () => searchButton.click())
    assert.equal(searchCalls.at(-1)?.keyword, '%alice%')
  })

  test('distinguishes the unfiltered and filtered invited-member empty states', async () => {
    const container = await renderComponent(
      <ReferralMembers
        error={false}
        loading={false}
        members={[]}
        onPageChange={() => undefined}
        onPageSizeChange={() => undefined}
        onRetry={() => undefined}
        onSearch={() => undefined}
        page={1}
        pageSize={10}
        qualifiedTopupQuota={500_000}
        total={0}
      />
    )

    assert.equal(container.textContent?.includes('暂无邀请成员'), true)
    assert.equal(
      container.textContent?.includes(
        '通过你的邀请链接注册的成员会显示在这里。'
      ),
      true
    )

    const searchInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索邀请成员"]'
    )
    assert.ok(searchInput)
    await act(async () => {
      setControlledInputValue(searchInput, 'missing')
    })
    const searchButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === '搜索'
    )
    assert.ok(searchButton)
    await act(async () => searchButton.click())
    assert.equal(
      container.textContent?.includes('没有符合筛选条件的成员'),
      true
    )
    assert.equal(
      container.textContent?.includes('请调整或重置筛选条件后重试。'),
      true
    )
  })

  test('uses a localized unavailable-member label instead of a numeric commission invitee ID', async () => {
    const records: ReferralCommission[] = [
      {
        id: 1,
        trade_no: 'commission-without-name',
        invitee_id: 74839261,
        invitee_name: '',
        payment_method: 'stripe',
        base_quota: 500_000,
        rate_bps: 500,
        reward_quota: 25_000,
        status: 'frozen',
        available_at: 1_725_100_000,
        create_time: 1_725_000_000,
      },
    ]
    const container = await renderComponent(
      <ReferralCommissionDetails
        loading={false}
        onPageChange={() => undefined}
        onPageSizeChange={() => undefined}
        onSearch={() => undefined}
        page={1}
        pageSize={10}
        paymentMethods={[]}
        records={records}
        total={records.length}
      />
    )

    assert.equal(container.textContent?.includes('成员信息不可用'), true)
    assert.equal(container.textContent?.includes('74839261'), false)
  })

  test('shows retry instead of an empty-member state when member loading fails', async () => {
    let retries = 0
    const container = await renderComponent(
      <ReferralMembers
        error
        loading={false}
        members={[]}
        onPageChange={() => undefined}
        onPageSizeChange={() => undefined}
        onRetry={() => {
          retries += 1
        }}
        onSearch={() => undefined}
        page={1}
        pageSize={10}
        qualifiedTopupQuota={500_000}
        total={0}
      />
    )

    assert.equal(container.textContent?.includes('加载失败'), true)
    assert.equal(container.textContent?.includes('暂无邀请成员'), false)
    const retryButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === '重试'
    )
    assert.ok(retryButton)
    await act(async () => retryButton.click())
    assert.equal(retries, 1)
  })

  test('keeps member rows visible and offers retry when refreshing existing data fails', async () => {
    let retries = 0
    const container = await renderComponent(
      <ReferralMembers
        error
        loading={false}
        members={members}
        onPageChange={() => undefined}
        onPageSizeChange={() => undefined}
        onRetry={() => {
          retries += 1
        }}
        onSearch={() => undefined}
        page={1}
        pageSize={10}
        qualifiedTopupQuota={500_000}
        total={members.length}
      />
    )

    const alert = container.querySelector('[role="alert"]')
    assert.ok(alert)
    assert.equal(alert.textContent?.includes('加载失败'), true)
    assert.equal(container.textContent?.includes('legacy-alice'), true)
    assert.equal(container.textContent?.includes('new-bob'), true)
    assert.equal(container.textContent?.includes('暂无邀请成员'), false)

    const retryButton = [...alert.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === '重试'
    )
    assert.ok(retryButton)
    await act(async () => retryButton.click())
    assert.equal(retries, 1)
  })

  test('keeps the referral overview and table headings translated in Traditional Chinese', async () => {
    await i18n.changeLanguage('zhTW')
    const container = await renderComponent(
      <>
        <ReferralOverview
          affiliateLink='https://example.com/register?aff=invite-code'
          loading={false}
          onTransfer={() => undefined}
          summary={summary}
        />
        <ReferralCommissionDetails
          loading={false}
          onPageChange={() => undefined}
          onPageSizeChange={() => undefined}
          onSearch={() => undefined}
          page={1}
          pageSize={10}
          paymentMethods={[]}
          records={[]}
          total={0}
        />
        <ReferralMembers
          error={false}
          loading={false}
          members={members}
          onPageChange={() => undefined}
          onPageSizeChange={() => undefined}
          onRetry={() => undefined}
          onSearch={() => undefined}
          page={1}
          pageSize={10}
          qualifiedTopupQuota={500_000}
          total={members.length}
        />
      </>
    )

    assert.ok(
      container.querySelector<HTMLInputElement>(
        'input[aria-label="你的邀請連結"]'
      )
    )
    assert.deepEqual(
      textValues(
        container.querySelectorAll('[data-referral-members] thead th')
      ),
      ['成員', '邀請類型', '累計線上儲值', '資格狀態', '邀請時間']
    )
    assert.deepEqual(
      textValues(
        container.querySelectorAll('[data-referral-commissions] thead th')
      ),
      ['成員', '來源', '儲值額度', '分成 · 比例', '時間', '狀態']
    )
    assert.equal(container.textContent?.includes('邀請成員'), true)
    assert.equal(container.textContent?.includes('舊邀請'), true)
    assert.equal(container.textContent?.includes('已達標'), true)
    assert.equal(
      container.textContent?.includes(
        '舊邀請和新邀請都會顯示。歷史成功線上儲值計入真實用戶資格，但不補發歷史分成；成員下一筆成功線上儲值起正常返佣。'
      ),
      true
    )
    assert.equal(
      container.textContent?.includes(
        '歷史分成不補發；成員下一筆成功線上儲值後，新的分成紀錄會顯示在這裡。'
      ),
      true
    )
    assert.equal(
      /\b(?:Member|Reward|Promotion|Top-up quota|Your referral link)\b/.test(
        container.textContent ?? ''
      ),
      false
    )
  })
})
