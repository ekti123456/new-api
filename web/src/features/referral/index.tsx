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
import { Share2, Users, Percent, WalletCards, TrendingUp } from 'lucide-react'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { SectionPageLayout } from '@/components/layout'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getReferralCommissions } from '@/features/wallet/api'
import { TransferDialog } from '@/features/wallet/components/dialogs/transfer-dialog'
import { useAffiliate } from '@/features/wallet/hooks'
import type { ReferralCommission } from '@/features/wallet/types'
import { formatQuota, formatTimestamp } from '@/lib/format'

const PAGE_SIZE = 10

export function ReferralPage() {
  const { t } = useTranslation()
  const { affiliateLink, summary, loading, transferring, transferQuota } =
    useAffiliate()
  const [records, setRecords] = useState<ReferralCommission[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [recordsLoading, setRecordsLoading] = useState(true)
  const [transferOpen, setTransferOpen] = useState(false)

  const fetchRecords = useCallback(async () => {
    try {
      setRecordsLoading(true)
      const response = await getReferralCommissions(page, PAGE_SIZE)
      if (response.success && response.data) {
        setRecords(response.data.items ?? [])
        setTotal(response.data.total ?? 0)
      }
    } finally {
      setRecordsLoading(false)
    }
  }, [page])

  useEffect(() => {
    fetchRecords()
  }, [fetchRecords])

  const currentRate = (summary?.rate_bps ?? 0) / 100
  const maxRate = (summary?.max_rate_bps ?? 0) / 100
  const tierProgress = summary
    ? ((summary.qualified_count % summary.users_per_tier) /
        summary.users_per_tier) *
      100
    : 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  let recordRows: ReactNode
  if (recordsLoading) {
    recordRows = (
      <TableRow>
        <TableCell colSpan={6} className='h-28 text-center'>
          {t('Loading...')}
        </TableCell>
      </TableRow>
    )
  } else if (records.length === 0) {
    recordRows = (
      <TableRow>
        <TableCell colSpan={6} className='h-28 text-center'>
          {t('No referral records yet')}
        </TableCell>
      </TableRow>
    )
  } else {
    recordRows = records.map((record) => (
      <TableRow key={record.id}>
        <TableCell>{record.invitee_name || record.invitee_id}</TableCell>
        <TableCell>{record.payment_method}</TableCell>
        <TableCell>{formatQuota(record.base_quota)}</TableCell>
        <TableCell>
          {formatQuota(record.reward_quota)} ·{' '}
          {(record.rate_bps / 100).toFixed(0)}%
        </TableCell>
        <TableCell>{formatTimestamp(record.create_time)}</TableCell>
        <TableCell>
          <StatusBadge
            label={
              record.status === 'available' ? t('Available') : t('Pending')
            }
            variant={record.status === 'available' ? 'success' : 'warning'}
            copyable={false}
          />
        </TableCell>
      </TableRow>
    ))
  }

  const handleTransfer = async (quota: number) => {
    const success = await transferQuota(quota)
    if (success) await fetchRecords()
    return success
  }

  const stats = [
    {
      label: t('Invites'),
      value: String(summary?.qualified_count ?? 0),
      icon: Users,
    },
    {
      label: t('Referral rate'),
      value: `${currentRate.toFixed(0)}%`,
      icon: Percent,
    },
    {
      label: t('Pending'),
      value: formatQuota(summary?.frozen_quota ?? 0),
      icon: WalletCards,
    },
    {
      label: t('Available Rewards'),
      value: formatQuota(summary?.available_quota ?? 0),
      icon: TrendingUp,
    },
    {
      label: t('Total Earned'),
      value: formatQuota(summary?.history_quota ?? 0),
      icon: Share2,
    },
  ]

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Promotion')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4'>
            <Card>
              <CardContent className='flex flex-col gap-3 p-4 lg:flex-row lg:items-center'>
                <div className='min-w-0 flex-1'>
                  <h2 className='font-semibold'>{t('Your referral link')}</h2>
                  <p className='text-muted-foreground text-sm'>
                    {t('Share this link to invite new users and earn rewards.')}
                  </p>
                </div>
                {loading ? (
                  <Skeleton className='h-10 w-full lg:w-[520px]' />
                ) : (
                  <div className='flex w-full gap-2 lg:w-[520px]'>
                    <Input
                      value={affiliateLink}
                      readOnly
                      className='font-mono'
                    />
                    <CopyButton value={affiliateLink} />
                  </div>
                )}
              </CardContent>
            </Card>

            <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
              {stats.map((stat) => (
                <Card key={stat.label}>
                  <CardContent className='flex items-center gap-3 p-4'>
                    <stat.icon className='text-primary size-5' />
                    <div className='min-w-0'>
                      <p className='text-muted-foreground truncate text-xs'>
                        {stat.label}
                      </p>
                      <p className='truncate text-xl font-semibold tabular-nums'>
                        {loading ? '-' : stat.value}
                      </p>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>

            <Card>
              <CardHeader className='flex-row items-center justify-between'>
                <div>
                  <CardTitle>{t('Commission rules')}</CardTitle>
                  <p className='text-muted-foreground mt-1 text-sm'>
                    {t(
                      'A qualified invite is a new referred user whose cumulative online top-ups reach {{quota}}. Redemption codes do not count.',
                      {
                        quota: formatQuota(summary?.qualified_topup_quota ?? 0),
                      }
                    )}
                  </p>
                </div>
                <Button
                  onClick={() => setTransferOpen(true)}
                  disabled={(summary?.available_quota ?? 0) <= 0}
                >
                  {t('Transfer to Balance')}
                </Button>
              </CardHeader>
              <CardContent className='space-y-2'>
                <div className='flex justify-between text-sm'>
                  <span>
                    {t('Current referral commission rate')}: {currentRate}%
                  </span>
                  <span>
                    {t('Maximum referral rate')}: {maxRate}%
                  </span>
                </div>
                <Progress value={currentRate >= maxRate ? 100 : tierProgress} />
                <p className='text-muted-foreground text-xs'>
                  {currentRate >= maxRate
                    ? t('Maximum referral rate reached')
                    : t(
                        '{{count}} more qualified invites until the next 1% increase',
                        {
                          count: summary?.next_tier_remaining ?? 0,
                        }
                      )}
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>{t('Referral details')}</CardTitle>
              </CardHeader>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Member')}</TableHead>
                      <TableHead>{t('Source')}</TableHead>
                      <TableHead>{t('Top-up quota')}</TableHead>
                      <TableHead>{t('Reward')}</TableHead>
                      <TableHead>{t('Time')}</TableHead>
                      <TableHead>{t('Status')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>{recordRows}</TableBody>
                </Table>
                <div className='mt-4 flex items-center justify-end gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page <= 1}
                    onClick={() => setPage((value) => value - 1)}
                  >
                    {t('Previous')}
                  </Button>
                  <span className='text-muted-foreground text-sm'>
                    {page} / {totalPages}
                  </span>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page >= totalPages}
                    onClick={() => setPage((value) => value + 1)}
                  >
                    {t('Next')}
                  </Button>
                </div>
              </CardContent>
            </Card>
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
