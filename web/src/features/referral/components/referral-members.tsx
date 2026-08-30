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
import {
  CircleAlert,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  RotateCcw,
  Search,
  UserRoundSearch,
} from 'lucide-react'
import { useState, type KeyboardEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
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
import type {
  ReferralMember,
  ReferralMemberFilters,
} from '@/features/wallet/types'
import { formatQuota, formatTimestamp } from '@/lib/format'
import { getPageNumbers } from '@/lib/utils'

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const
const LOADING_ROW_KEYS = ['loading-1', 'loading-2', 'loading-3']
const LOADING_CELL_KEYS = [
  'member',
  'invitation-type',
  'top-up',
  'status',
  'invited-at',
]

interface ReferralMembersProps {
  error: boolean
  loading: boolean
  members: ReferralMember[]
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onRetry: () => void
  onSearch: (filters: ReferralMemberFilters) => void
  page: number
  pageSize: number
  qualifiedTopupQuota: number
  total: number
}

function referralMemberSearchPattern(keyword: string): string | undefined {
  const normalized = keyword.trim().replaceAll('%', '')
  if (!normalized) return undefined
  const canUseFuzzySearch =
    normalized.length > 1 || (normalized.codePointAt(0) ?? 0) > 127
  return canUseFuzzySearch ? `%${normalized}%` : normalized
}

export function ReferralMembers(props: ReferralMembersProps) {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const [status, setStatus] = useState('all')
  const [hasActiveFilters, setHasActiveFilters] = useState(false)
  const totalPages = Math.max(1, Math.ceil(props.total / props.pageSize))
  const pageNumbers = getPageNumbers(props.page, totalPages)
  const pageItems = pageNumbers.map((pageNumber, index) => ({
    key:
      pageNumber === '...'
        ? `ellipsis-${String(pageNumbers[index - 1])}-${String(pageNumbers[index + 1])}`
        : `page-${pageNumber}`,
    pageNumber,
  }))
  const statusItems = [
    { label: t('All qualification statuses'), value: 'all' },
    { label: t('Qualified members'), value: 'qualified' },
    { label: t('Pending qualification'), value: 'pending' },
  ]
  const emptyTitle = hasActiveFilters
    ? t('No members match the filters')
    : t('No invited members yet')
  const emptyDescription = hasActiveFilters
    ? t('Adjust or reset the filters and try again.')
    : t('Members who register through your referral link will appear here.')

  const handleSearch = () => {
    const keywordFilter = referralMemberSearchPattern(keyword)
    setHasActiveFilters(Boolean(keywordFilter) || status !== 'all')
    props.onSearch({
      keyword: keywordFilter,
      status:
        status === 'qualified' || status === 'pending' ? status : undefined,
    })
  }

  const handleSearchKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') handleSearch()
  }

  const handleReset = () => {
    setKeyword('')
    setStatus('all')
    setHasActiveFilters(false)
    props.onSearch({})
  }

  let mobileMembers: ReactNode
  if (props.loading) {
    mobileMembers = (
      <div className='divide-y'>
        {LOADING_ROW_KEYS.map((rowKey) => (
          <div key={rowKey} className='space-y-3 p-3'>
            <div className='flex items-center justify-between gap-3'>
              <Skeleton className='h-4 w-28' />
              <Skeleton className='h-5 w-20' />
            </div>
            <div className='grid grid-cols-2 gap-3'>
              <Skeleton className='h-8 w-full' />
              <Skeleton className='h-8 w-full' />
            </div>
          </div>
        ))}
      </div>
    )
  } else if (props.error && props.members.length === 0) {
    mobileMembers = (
      <div className='text-muted-foreground flex h-40 flex-col items-center justify-center px-4 text-center'>
        <CircleAlert className='mb-2 size-8 opacity-40' />
        <p className='text-sm font-medium'>{t('Failed to load')}</p>
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
  } else if (props.members.length === 0) {
    mobileMembers = (
      <div className='text-muted-foreground flex h-40 flex-col items-center justify-center px-4 text-center'>
        <UserRoundSearch className='mb-2 size-8 opacity-40' />
        <p className='text-sm font-medium'>{emptyTitle}</p>
        <p className='mt-1 text-xs'>{emptyDescription}</p>
      </div>
    )
  } else {
    mobileMembers = (
      <div className='divide-y'>
        {props.members.map((member) => {
          const qualified = member.referral_qualified_at > 0
          const remainingQuota =
            props.qualifiedTopupQuota > 0
              ? Math.max(
                  0,
                  props.qualifiedTopupQuota - member.referral_topup_quota
                )
              : null
          return (
            <article key={member.id} className='space-y-3 p-3'>
              <div className='flex min-w-0 items-start justify-between gap-3'>
                <div className='min-w-0'>
                  <p className='truncate font-medium'>{member.username}</p>
                </div>
                <div className='flex shrink-0 flex-wrap justify-end gap-1'>
                  <StatusBadge
                    label={
                      member.legacy
                        ? t('Historical invitation')
                        : t('New invitation')
                    }
                    variant={member.legacy ? 'neutral' : 'info'}
                    copyable={false}
                  />
                  <StatusBadge
                    label={
                      qualified
                        ? t('Qualified member')
                        : t('Pending qualification')
                    }
                    variant={qualified ? 'success' : 'warning'}
                    copyable={false}
                  />
                </div>
              </div>
              <div className='grid grid-cols-2 gap-3'>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Cumulative online top-up')}
                  </p>
                  <p className='truncate font-mono text-sm font-semibold'>
                    {formatQuota(member.referral_topup_quota)}
                  </p>
                  {!qualified && remainingQuota !== null ? (
                    <p className='text-muted-foreground truncate text-xs'>
                      {t('{{quota}} remaining to qualify', {
                        quota: formatQuota(remainingQuota),
                      })}
                    </p>
                  ) : null}
                </div>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Invitation time')}
                  </p>
                  <p className='text-muted-foreground truncate text-xs tabular-nums'>
                    {formatTimestamp(member.created_at)}
                  </p>
                </div>
              </div>
            </article>
          )
        })}
      </div>
    )
  }

  let tableRows: ReactNode
  if (props.loading) {
    tableRows = LOADING_ROW_KEYS.map((rowKey) => (
      <TableRow key={rowKey}>
        {LOADING_CELL_KEYS.map((cellKey) => (
          <TableCell key={cellKey}>
            <Skeleton className='h-4 w-full max-w-28' />
          </TableCell>
        ))}
      </TableRow>
    ))
  } else if (props.error && props.members.length === 0) {
    tableRows = (
      <TableRow>
        <TableCell colSpan={5} className='h-40 text-center'>
          <div className='text-muted-foreground flex flex-col items-center'>
            <CircleAlert className='mb-2 size-8 opacity-40' />
            <p className='text-sm font-medium'>{t('Failed to load')}</p>
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
  } else if (props.members.length === 0) {
    tableRows = (
      <TableRow>
        <TableCell colSpan={5} className='h-40 text-center'>
          <div className='text-muted-foreground flex flex-col items-center'>
            <UserRoundSearch className='mb-2 size-8 opacity-40' />
            <p className='text-sm font-medium'>{emptyTitle}</p>
            <p className='mt-1 text-xs'>{emptyDescription}</p>
          </div>
        </TableCell>
      </TableRow>
    )
  } else {
    tableRows = props.members.map((member) => {
      const qualified = member.referral_qualified_at > 0
      const remainingQuota =
        props.qualifiedTopupQuota > 0
          ? Math.max(0, props.qualifiedTopupQuota - member.referral_topup_quota)
          : null
      return (
        <TableRow key={member.id}>
          <TableCell className='max-w-52'>
            <p className='truncate font-medium' title={member.username}>
              {member.username}
            </p>
          </TableCell>
          <TableCell>
            <StatusBadge
              label={
                member.legacy ? t('Historical invitation') : t('New invitation')
              }
              variant={member.legacy ? 'neutral' : 'info'}
              copyable={false}
            />
          </TableCell>
          <TableCell>
            <p className='font-mono font-semibold'>
              {formatQuota(member.referral_topup_quota)}
            </p>
          </TableCell>
          <TableCell>
            <StatusBadge
              label={
                qualified ? t('Qualified member') : t('Pending qualification')
              }
              variant={qualified ? 'success' : 'warning'}
              copyable={false}
            />
            {!qualified && remainingQuota !== null ? (
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('{{quota}} remaining to qualify', {
                  quota: formatQuota(remainingQuota),
                })}
              </p>
            ) : null}
          </TableCell>
          <TableCell className='text-muted-foreground'>
            {formatTimestamp(member.created_at)}
          </TableCell>
        </TableRow>
      )
    })
  }

  return (
    <Card data-card-hover='false' className='gap-0 py-0' data-referral-members>
      <CardHeader className='gap-1 border-b p-4 sm:p-5'>
        <CardTitle className='text-base font-semibold'>
          {t('Invited member list')}
        </CardTitle>
        <CardDescription>
          {t(
            "Old and new invitations are both shown here. Historical successful online top-ups count toward qualification, but past commissions are not backfilled; the member's next successful online top-up earns commission normally."
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='p-4 sm:p-5'>
        <div className='mb-3 flex flex-col gap-2 lg:flex-row lg:items-center'>
          <div className='relative min-w-0 flex-1 lg:max-w-[360px]'>
            <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
            <Input
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              onKeyDown={handleSearchKeyDown}
              placeholder={t('Search invited member')}
              aria-label={t('Search invited member')}
              className='pl-9'
            />
          </div>

          <Select
            items={statusItems}
            value={status}
            onValueChange={(value) => value !== null && setStatus(value)}
          >
            <SelectTrigger className='w-full sm:w-48'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {statusItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>

          <div className='flex items-center gap-2 lg:ml-auto'>
            <Button
              type='button'
              variant='ghost'
              className='flex-1 lg:flex-none'
              onClick={handleReset}
            >
              <RotateCcw />
              {t('Reset')}
            </Button>
            <Button
              type='button'
              className='flex-1 lg:flex-none'
              onClick={handleSearch}
            >
              <Search />
              {t('Search')}
            </Button>
          </div>
        </div>

        {props.error && props.members.length > 0 ? (
          <div
            role='alert'
            className='border-warning/30 bg-warning/5 text-muted-foreground mb-3 flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm'
          >
            <CircleAlert className='text-warning size-4 shrink-0' />
            <span>{t('Failed to load')}</span>
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

        <div className='overflow-hidden rounded-lg border lg:hidden'>
          {mobileMembers}
        </div>

        <div className='hidden overflow-hidden rounded-lg border lg:block'>
          <Table className='min-w-[760px]'>
            <TableHeader className='bg-muted/40'>
              <TableRow className='hover:bg-transparent'>
                <TableHead>{t('Member')}</TableHead>
                <TableHead>{t('Invitation type')}</TableHead>
                <TableHead>{t('Cumulative online top-up')}</TableHead>
                <TableHead>{t('Qualification status')}</TableHead>
                <TableHead>{t('Invitation time')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>{tableRows}</TableBody>
          </Table>
        </div>

        <div className='mt-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
          <div className='flex items-center gap-3'>
            <div className='flex items-baseline gap-1.5 text-sm font-medium whitespace-nowrap'>
              <span className='text-muted-foreground'>{t('Total:')}</span>
              <span className='tabular-nums'>
                {props.total.toLocaleString()}
              </span>
            </div>
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
              <SelectTrigger className='w-[72px] tabular-nums'>
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
            <span className='text-muted-foreground hidden text-sm sm:inline'>
              {t('Rows per page')}
            </span>
          </div>

          <div className='flex items-center justify-end gap-1.5'>
            <Button
              type='button'
              variant='outline'
              size='icon'
              onClick={() => props.onPageChange(1)}
              disabled={props.page <= 1}
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
              disabled={props.page <= 1}
            >
              <ChevronLeft />
              <span className='sr-only'>{t('Go to previous page')}</span>
            </Button>

            {pageItems.map((item) =>
              item.pageNumber === '...' ? (
                <span
                  key={item.key}
                  className='text-muted-foreground px-1 text-sm'
                >
                  ...
                </span>
              ) : (
                <Button
                  key={item.key}
                  type='button'
                  variant={
                    props.page === item.pageNumber ? 'default' : 'outline'
                  }
                  className='h-8 min-w-8 px-2 tabular-nums'
                  onClick={() => props.onPageChange(Number(item.pageNumber))}
                >
                  {item.pageNumber}
                  <span className='sr-only'>
                    {t('Go to page {{page}}', { page: item.pageNumber })}
                  </span>
                </Button>
              )
            )}

            <Button
              type='button'
              variant='outline'
              size='icon'
              onClick={() => props.onPageChange(props.page + 1)}
              disabled={props.page >= totalPages}
            >
              <ChevronRight />
              <span className='sr-only'>{t('Go to next page')}</span>
            </Button>
            <Button
              type='button'
              variant='outline'
              size='icon'
              onClick={() => props.onPageChange(totalPages)}
              disabled={props.page >= totalPages}
              className='hidden sm:inline-flex'
            >
              <ChevronsRight />
              <span className='sr-only'>{t('Go to last page')}</span>
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
