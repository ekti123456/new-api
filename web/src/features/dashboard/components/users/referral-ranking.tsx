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
import { useQuery } from '@tanstack/react-query'
import {
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  CircleAlert,
  RefreshCw,
  Trophy,
  UsersRound,
} from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { UserInfoDialog } from '@/features/usage-logs/components/dialogs/user-info-dialog'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuota } from '@/lib/format'
import { cn, getPageNumbers } from '@/lib/utils'

import { getReferralRankings } from '../../api'
import type {
  ReferralRankingItem,
  ReferralRankingPage,
  ReferralRankingPeriod,
} from '../../types'
import {
  getLocalTodayRange,
  getMillisecondsUntilNextLocalDay,
} from './referral-ranking-time'

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const
const LOADING_ROWS = ['referral-rank-1', 'referral-rank-2', 'referral-rank-3']
const LOADING_CELLS = [
  'rank',
  'inviter',
  'invited',
  'qualified',
  'topup',
  'commission',
]

export interface ReferralRankingPanelProps {
  data?: ReferralRankingPage
  error: boolean
  fetching: boolean
  loading: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onPeriodChange: (period: ReferralRankingPeriod) => void
  onRetry: () => void
  onUserOpen: (userId: number) => void
  page: number
  pageSize: number
  period: ReferralRankingPeriod
}

function getInviterName(item: ReferralRankingItem) {
  return (
    item.display_name?.trim() || item.username?.trim() || `#${item.user_id}`
  )
}

function RankingBadge(props: { rank: number }) {
  return (
    <span
      className={cn(
        'inline-flex size-7 items-center justify-center rounded-full font-mono text-xs font-bold tabular-nums',
        props.rank === 1 &&
          'bg-amber-500/15 text-amber-700 dark:text-amber-300',
        props.rank === 2 &&
          'bg-slate-500/15 text-slate-600 dark:text-slate-300',
        props.rank === 3 &&
          'bg-orange-500/15 text-orange-700 dark:text-orange-300',
        props.rank > 3 && 'bg-muted text-muted-foreground'
      )}
    >
      {props.rank}
    </span>
  )
}

export function ReferralRankingPanel(props: ReferralRankingPanelProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const total = props.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / props.pageSize))
  const pageNumbers = getPageNumbers(props.page, totalPages)
  const pageItems = pageNumbers.map((pageNumber, index) => ({
    key:
      pageNumber === '...'
        ? `ellipsis-${String(pageNumbers[index - 1])}-${String(pageNumbers[index + 1])}`
        : `page-${pageNumber}`,
    pageNumber,
  }))
  const items = props.data?.items ?? []
  const isToday = props.period === 'today'
  const columnLabels = isToday
    ? {
        invited: t('Invitations today'),
        qualified: t('Qualified users today'),
        topup: t('Top-up amount today'),
        commission: t('Commission today'),
      }
    : {
        invited: t('Total invitations'),
        qualified: t('Qualified users'),
        topup: t('Total top-up amount'),
        commission: t('Total commission'),
      }

  let tableRows: ReactNode
  if (props.loading) {
    tableRows = LOADING_ROWS.map((rowKey) => (
      <TableRow key={rowKey}>
        {LOADING_CELLS.map((cellKey) => (
          <TableCell key={cellKey}>
            <Skeleton className='h-4 w-full max-w-28' />
          </TableCell>
        ))}
      </TableRow>
    ))
  } else if (props.error && items.length === 0) {
    tableRows = (
      <TableRow>
        <TableCell colSpan={6} className='h-40 text-center'>
          <div className='text-muted-foreground flex flex-col items-center'>
            <CircleAlert className='mb-2 size-8 opacity-40' />
            <p className='text-sm font-medium'>
              {t('Unable to load referral rankings')}
            </p>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='mt-3'
              onClick={props.onRetry}
            >
              {t('Retry')}
            </Button>
          </div>
        </TableCell>
      </TableRow>
    )
  } else if (items.length === 0) {
    tableRows = (
      <TableRow>
        <TableCell colSpan={6} className='h-40 text-center'>
          <div className='text-muted-foreground flex flex-col items-center'>
            <UsersRound className='mb-2 size-8 opacity-40' />
            <p className='text-sm font-medium'>
              {t('No referral ranking data')}
            </p>
            <p className='mt-1 text-xs'>
              {t('No inviters were recorded for this period.')}
            </p>
          </div>
        </TableCell>
      </TableRow>
    )
  } else {
    tableRows = items.map((item, index) => {
      const rank = (props.page - 1) * props.pageSize + index + 1
      const name = getInviterName(item)
      const showUsername =
        item.username?.trim() && item.username.trim() !== name
      return (
        <TableRow key={item.user_id}>
          <TableCell>
            <RankingBadge rank={rank} />
          </TableCell>
          <TableCell className='max-w-64'>
            <button
              type='button'
              className='block max-w-full truncate text-left font-semibold hover:underline'
              title={name}
              onClick={() => props.onUserOpen(item.user_id)}
            >
              {name}
            </button>
            <span className='text-muted-foreground block truncate font-mono text-xs'>
              {showUsername ? `@${item.username.trim()} · ` : ''}#{item.user_id}
            </span>
          </TableCell>
          <TableCell className='font-semibold'>
            {item.invited_count.toLocaleString(locale)}
          </TableCell>
          <TableCell className='font-semibold'>
            {item.qualified_count.toLocaleString(locale)}
          </TableCell>
          <TableCell className='font-mono font-semibold'>
            {formatQuota(item.topup_quota)}
          </TableCell>
          <TableCell>
            <span className='block font-mono font-semibold'>
              {formatQuota(item.commission_quota)}
            </span>
            <span className='text-muted-foreground text-xs'>
              {t('{{count}} commission records', {
                count: item.commission_count.toLocaleString(locale),
              })}
            </span>
          </TableCell>
        </TableRow>
      )
    })
  }

  let mobileRows: ReactNode
  if (props.loading) {
    mobileRows = (
      <div className='divide-y'>
        {LOADING_ROWS.map((key) => (
          <div key={key} className='space-y-3 p-4'>
            <div className='flex items-center gap-3'>
              <Skeleton className='size-7 rounded-full' />
              <Skeleton className='h-4 w-32' />
            </div>
            <div className='grid grid-cols-2 gap-3'>
              <Skeleton className='h-10 w-full' />
              <Skeleton className='h-10 w-full' />
              <Skeleton className='h-10 w-full' />
              <Skeleton className='h-10 w-full' />
            </div>
          </div>
        ))}
      </div>
    )
  } else if (props.error && items.length === 0) {
    mobileRows = (
      <div className='text-muted-foreground flex h-40 flex-col items-center justify-center px-4 text-center'>
        <CircleAlert className='mb-2 size-8 opacity-40' />
        <p className='text-sm font-medium'>
          {t('Unable to load referral rankings')}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='mt-3'
          onClick={props.onRetry}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (items.length === 0) {
    mobileRows = (
      <div className='text-muted-foreground flex h-40 flex-col items-center justify-center px-4 text-center'>
        <UsersRound className='mb-2 size-8 opacity-40' />
        <p className='text-sm font-medium'>{t('No referral ranking data')}</p>
        <p className='mt-1 text-xs'>
          {t('No inviters were recorded for this period.')}
        </p>
      </div>
    )
  } else {
    mobileRows = (
      <div className='divide-y'>
        {items.map((item, index) => {
          const rank = (props.page - 1) * props.pageSize + index + 1
          const name = getInviterName(item)
          const showUsername =
            item.username?.trim() && item.username.trim() !== name
          const metrics = [
            {
              key: 'invited',
              label: columnLabels.invited,
              value: item.invited_count.toLocaleString(locale),
            },
            {
              key: 'qualified',
              label: columnLabels.qualified,
              value: item.qualified_count.toLocaleString(locale),
            },
            {
              key: 'topup',
              label: columnLabels.topup,
              value: formatQuota(item.topup_quota),
            },
            {
              key: 'commission',
              label: columnLabels.commission,
              value: formatQuota(item.commission_quota),
              detail: t('{{count}} commission records', {
                count: item.commission_count.toLocaleString(locale),
              }),
            },
          ]
          return (
            <article key={item.user_id} className='space-y-3 p-4'>
              <div className='flex min-w-0 items-center gap-3'>
                <RankingBadge rank={rank} />
                <div className='min-w-0'>
                  <button
                    type='button'
                    className='block max-w-full truncate text-left font-semibold hover:underline'
                    title={name}
                    onClick={() => props.onUserOpen(item.user_id)}
                  >
                    {name}
                  </button>
                  <span className='text-muted-foreground block truncate font-mono text-xs'>
                    {showUsername ? `@${item.username.trim()} · ` : ''}#
                    {item.user_id}
                  </span>
                </div>
              </div>
              <div className='grid grid-cols-2 gap-x-4 gap-y-3'>
                {metrics.map((metric) => (
                  <div key={metric.key} className='min-w-0'>
                    <p className='text-muted-foreground truncate text-xs'>
                      {metric.label}
                    </p>
                    <p className='truncate font-mono text-sm font-semibold'>
                      {metric.value}
                    </p>
                    {metric.detail ? (
                      <p className='text-muted-foreground truncate text-xs'>
                        {metric.detail}
                      </p>
                    ) : null}
                  </div>
                ))}
              </div>
            </article>
          )
        })}
      </div>
    )
  }

  return (
    <div data-referral-ranking className='overflow-hidden rounded-lg border'>
      <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
        <div className='flex min-w-0 items-start gap-2'>
          <IconBadge tone='warning' size='sm'>
            <Trophy />
          </IconBadge>
          <div className='min-w-0'>
            <div className='text-sm font-semibold'>{t('Referral ranking')}</div>
            <div className='text-muted-foreground text-xs'>
              {isToday
                ? t(
                    "Today's ranking covers users invited during your local calendar day."
                  )
                : t('Rank inviters by qualified users and referred top-ups.')}
            </div>
          </div>
        </div>

        <div className='flex items-center gap-2 self-end sm:self-auto'>
          {props.fetching && !props.loading ? (
            <RefreshCw className='text-muted-foreground size-3.5 animate-spin' />
          ) : null}
          <Tabs
            value={props.period}
            onValueChange={(value) => {
              if (value === 'all' || value === 'today') {
                props.onPeriodChange(value)
              }
            }}
          >
            <TabsList aria-label={t('Referral ranking period')}>
              <TabsTrigger value='all' className='px-3 text-xs'>
                {t('All-time')}
              </TabsTrigger>
              <TabsTrigger value='today' className='px-3 text-xs'>
                {t('Today')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
      </div>

      {props.error && items.length > 0 ? (
        <div
          role='alert'
          className='border-warning/30 bg-warning/5 text-muted-foreground m-3 flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm sm:mx-5'
        >
          <CircleAlert className='text-warning size-4 shrink-0' />
          <span>{t('Unable to load referral rankings')}</span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='ml-auto h-7 px-2'
            onClick={props.onRetry}
          >
            {t('Retry')}
          </Button>
        </div>
      ) : null}

      <div className='lg:hidden'>{mobileRows}</div>

      <div className='hidden lg:block'>
        <Table className='min-w-[860px]'>
          <TableHeader className='bg-muted/40'>
            <TableRow className='hover:bg-transparent'>
              <TableHead className='w-16'>{t('Rank')}</TableHead>
              <TableHead>{t('Inviter')}</TableHead>
              <TableHead>{columnLabels.invited}</TableHead>
              <TableHead>{columnLabels.qualified}</TableHead>
              <TableHead>{columnLabels.topup}</TableHead>
              <TableHead>{columnLabels.commission}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>{tableRows}</TableBody>
        </Table>
      </div>

      <div className='flex flex-col gap-3 border-t px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
        <div className='flex items-center gap-3'>
          <span className='text-muted-foreground text-sm whitespace-nowrap'>
            {t('Total inviters')}:{' '}
            <strong className='text-foreground tabular-nums'>
              {total.toLocaleString(locale)}
            </strong>
          </span>
          <Select
            items={PAGE_SIZE_OPTIONS.map((pageSize) => ({
              label: pageSize,
              value: `${pageSize}`,
            }))}
            value={`${props.pageSize}`}
            onValueChange={(value) => {
              if (value !== null) props.onPageSizeChange(Number(value))
            }}
          >
            <SelectTrigger
              className='w-[72px] tabular-nums'
              aria-label={t('Rows per page')}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent side='top' alignItemWithTrigger={false}>
              <SelectGroup>
                {PAGE_SIZE_OPTIONS.map((pageSize) => (
                  <SelectItem key={pageSize} value={`${pageSize}`}>
                    {pageSize}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <div className='flex items-center justify-end gap-1.5'>
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => props.onPageChange(1)}
            disabled={props.fetching || props.page <= 1}
            className='hidden sm:inline-flex'
          >
            <ChevronsLeft />
            <span className='sr-only'>{t('Go to first page')}</span>
          </Button>
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => props.onPageChange(props.page - 1)}
            disabled={props.fetching || props.page <= 1}
          >
            <ChevronLeft />
            <span className='sr-only'>{t('Go to previous page')}</span>
          </Button>

          {pageItems.map(({ key, pageNumber }) => {
            if (pageNumber === '...') {
              return (
                <span key={key} className='text-muted-foreground px-1 text-sm'>
                  ...
                </span>
              )
            }
            return (
              <Button
                key={key}
                type='button'
                variant={props.page === pageNumber ? 'default' : 'outline'}
                className='h-8 min-w-8 px-2 tabular-nums'
                disabled={props.fetching}
                onClick={() => props.onPageChange(Number(pageNumber))}
              >
                {pageNumber}
                <span className='sr-only'>
                  {t('Go to page {{page}}', { page: pageNumber })}
                </span>
              </Button>
            )
          })}

          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => props.onPageChange(props.page + 1)}
            disabled={props.fetching || props.page >= totalPages}
          >
            <ChevronRight />
            <span className='sr-only'>{t('Go to next page')}</span>
          </Button>
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => props.onPageChange(totalPages)}
            disabled={props.fetching || props.page >= totalPages}
            className='hidden sm:inline-flex'
          >
            <ChevronsRight />
            <span className='sr-only'>{t('Go to last page')}</span>
          </Button>
        </div>
      </div>
    </div>
  )
}

export function ReferralRanking() {
  const [period, setPeriod] = useState<ReferralRankingPeriod>('all')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [selectedUserId, setSelectedUserId] = useState<number | null>(null)
  const [userDialogOpen, setUserDialogOpen] = useState(false)
  const [localDayRevision, setLocalDayRevision] = useState(0)
  const todayRange = period === 'today' ? getLocalTodayRange() : undefined

  useEffect(() => {
    if (period !== 'today') return
    const timeoutId = window.setTimeout(
      () => setLocalDayRevision((revision) => revision + 1),
      getMillisecondsUntilNextLocalDay()
    )
    return () => window.clearTimeout(timeoutId)
  }, [localDayRevision, period])

  const query = useQuery({
    queryKey: [
      'dashboard',
      'referral-rankings',
      period,
      page,
      pageSize,
      todayRange?.startTimestamp,
      todayRange?.endTimestamp,
    ],
    queryFn: async () => {
      const response = await getReferralRankings({
        period,
        page,
        pageSize,
        startTimestamp: todayRange?.startTimestamp,
        endTimestamp: todayRange?.endTimestamp,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || 'Unable to load referral rankings')
      }
      return response.data
    },
    retry: false,
    staleTime: 30_000,
  })

  useEffect(() => {
    if (!query.data) return
    const totalPages = Math.max(1, Math.ceil(query.data.total / pageSize))
    if (page > totalPages) setPage(totalPages)
  }, [page, pageSize, query.data])

  const handlePeriodChange = (nextPeriod: ReferralRankingPeriod) => {
    setPeriod(nextPeriod)
    setPage(1)
  }

  const handlePageSizeChange = (nextPageSize: number) => {
    setPageSize(nextPageSize)
    setPage(1)
  }

  const handleUserOpen = (userId: number) => {
    setSelectedUserId(userId)
    setUserDialogOpen(true)
  }

  return (
    <>
      <ReferralRankingPanel
        data={query.data}
        error={query.isError}
        fetching={query.isFetching}
        loading={query.isLoading}
        onPageChange={setPage}
        onPageSizeChange={handlePageSizeChange}
        onPeriodChange={handlePeriodChange}
        onRetry={() => void query.refetch()}
        onUserOpen={handleUserOpen}
        page={page}
        pageSize={pageSize}
        period={period}
      />
      <UserInfoDialog
        userId={selectedUserId}
        open={userDialogOpen}
        onOpenChange={setUserDialogOpen}
      />
    </>
  )
}
