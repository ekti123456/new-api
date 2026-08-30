/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import {
  getReferralCommissions,
  getReferralMembers,
} from '@/features/wallet/api'
import { TransferDialog } from '@/features/wallet/components/dialogs/transfer-dialog'
import { useAffiliate, useTopupInfo } from '@/features/wallet/hooks'
import type {
  PaymentMethod,
  ReferralCommission,
  ReferralCommissionFilters,
  ReferralMember,
  ReferralMemberFilters,
} from '@/features/wallet/types'

import { ReferralCommissionDetails } from './components/referral-commission-details'
import { ReferralMembers } from './components/referral-members'
import { ReferralOverview } from './components/referral-overview'

const DEFAULT_PAGE_SIZE = 10

export function ReferralPage() {
  const { t } = useTranslation()
  const { affiliateLink, summary, loading, transferring, transferQuota } =
    useAffiliate()
  const { topupInfo } = useTopupInfo()
  const [records, setRecords] = useState<ReferralCommission[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [filters, setFilters] = useState<ReferralCommissionFilters>({})
  const [recordsLoading, setRecordsLoading] = useState(true)
  const [members, setMembers] = useState<ReferralMember[]>([])
  const [memberTotal, setMemberTotal] = useState(0)
  const [memberPage, setMemberPage] = useState(1)
  const [memberPageSize, setMemberPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [memberFilters, setMemberFilters] = useState<ReferralMemberFilters>({})
  const [membersLoading, setMembersLoading] = useState(true)
  const [membersError, setMembersError] = useState(false)
  const [transferOpen, setTransferOpen] = useState(false)
  const requestIdRef = useRef(0)
  const memberRequestIdRef = useRef(0)
  const paymentMethods = useMemo(() => {
    const methods = new Map<string, PaymentMethod>()
    for (const method of topupInfo?.pay_methods ?? []) {
      methods.set(method.type, method)
    }
    const addProvider = (
      enabled: boolean | undefined,
      method: PaymentMethod
    ) => {
      if (enabled && !methods.has(method.type)) methods.set(method.type, method)
    }
    addProvider(topupInfo?.enable_creem_topup, {
      name: 'Creem',
      type: 'creem',
    })
    addProvider(topupInfo?.enable_waffo_topup, {
      name: 'Waffo',
      type: 'waffo',
    })
    addProvider(topupInfo?.enable_waffo_pancake_topup, {
      name: 'Waffo Pancake',
      type: 'waffo_pancake',
    })
    return [...methods.values()]
  }, [topupInfo])

  const fetchRecords = useCallback(async () => {
    const requestId = ++requestIdRef.current
    try {
      setRecordsLoading(true)
      const response = await getReferralCommissions(page, pageSize, filters)
      if (
        requestId === requestIdRef.current &&
        response.success &&
        response.data
      ) {
        setRecords(response.data.items ?? [])
        setTotal(response.data.total ?? 0)
      }
    } finally {
      if (requestId === requestIdRef.current) setRecordsLoading(false)
    }
  }, [filters, page, pageSize])

  useEffect(() => {
    fetchRecords()
  }, [fetchRecords])

  const fetchMembers = useCallback(async () => {
    const requestId = ++memberRequestIdRef.current
    try {
      setMembersLoading(true)
      const response = await getReferralMembers(
        memberPage,
        memberPageSize,
        memberFilters
      )
      if (
        requestId === memberRequestIdRef.current &&
        response.success &&
        response.data
      ) {
        setMembers(response.data.items ?? [])
        setMemberTotal(response.data.total ?? 0)
        setMembersError(false)
      } else if (requestId === memberRequestIdRef.current) {
        setMembersError(true)
      }
    } catch {
      if (requestId === memberRequestIdRef.current) setMembersError(true)
    } finally {
      if (requestId === memberRequestIdRef.current) setMembersLoading(false)
    }
  }, [memberFilters, memberPage, memberPageSize])

  useEffect(() => {
    fetchMembers()
  }, [fetchMembers])

  const handleTransfer = async (quota: number) => {
    const success = await transferQuota(quota)
    if (success) await fetchRecords()
    return success
  }

  const handleSearch = (nextFilters: ReferralCommissionFilters) => {
    setPage(1)
    setFilters(nextFilters)
  }

  const handlePageSizeChange = (nextPageSize: number) => {
    setPage(1)
    setPageSize(nextPageSize)
  }

  const handleMemberSearch = (nextFilters: ReferralMemberFilters) => {
    setMemberPage(1)
    setMemberFilters(nextFilters)
  }

  const handleMemberPageSizeChange = (nextPageSize: number) => {
    setMemberPage(1)
    setMemberPageSize(nextPageSize)
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Promotion')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-[1600px] flex-col gap-3 sm:gap-4'>
            <p className='text-muted-foreground text-sm'>
              {t('Invite members, earn commissions, and transfer rewards.')}
            </p>

            <ReferralOverview
              affiliateLink={affiliateLink}
              loading={loading}
              summary={summary}
              onTransfer={() => setTransferOpen(true)}
            />

            <ReferralMembers
              members={members}
              total={memberTotal}
              page={memberPage}
              pageSize={memberPageSize}
              qualifiedTopupQuota={summary?.qualified_topup_quota ?? 0}
              loading={membersLoading}
              error={membersError}
              onSearch={handleMemberSearch}
              onRetry={fetchMembers}
              onPageChange={setMemberPage}
              onPageSizeChange={handleMemberPageSizeChange}
            />

            <ReferralCommissionDetails
              records={records}
              paymentMethods={paymentMethods}
              total={total}
              page={page}
              pageSize={pageSize}
              loading={recordsLoading}
              onSearch={handleSearch}
              onPageChange={setPage}
              onPageSizeChange={handlePageSizeChange}
            />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onConfirm={handleTransfer}
        availableQuota={summary?.available_quota ?? 0}
        transferring={transferring}
      />
    </>
  )
}
