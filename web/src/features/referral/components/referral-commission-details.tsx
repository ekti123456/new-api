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
  CalendarDays,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  ReceiptText,
  RotateCcw,
  Search,
} from 'lucide-react'
import { useMemo, useState, type KeyboardEvent, type ReactNode } from 'react'
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
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
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
import { getPaymentMethodName } from '@/features/wallet/lib/billing'
import type {
  PaymentMethod,
  ReferralCommission,
  ReferralCommissionFilters,
} from '@/features/wallet/types'
import dayjs from '@/lib/dayjs'
import { formatQuota, formatTimestamp } from '@/lib/format'
import { cn, getPageNumbers } from '@/lib/utils'

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const
const LOADING_ROW_KEYS = ['loading-1', 'loading-2', 'loading-3', 'loading-4']
const LOADING_CELL_KEYS = [
  'member',
  'source',
  'topup',
  'commission',
  'time',
  'status',
]

interface DateRange {
  end?: Date
  start?: Date
}

interface ReferralCommissionDetailsProps {
  loading: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onSearch: (filters: ReferralCommissionFilters) => void
  page: number
  pageSize: number
  paymentMethods: PaymentMethod[]
  records: ReferralCommission[]
  total: number
}

interface ReferralDateRangeFilterProps {
  onChange: (range: DateRange) => void
  value: DateRange
}

function toDateInputValue(date?: Date): string {
  return date ? dayjs(date).format('YYYY-MM-DDTHH:mm') : ''
}

function fromDateInputValue(value: string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date
}

function ReferralDateRangeFilter(props: ReferralDateRangeFilterProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [draftStart, setDraftStart] = useState(
    toDateInputValue(props.value.start)
  )
  const [draftEnd, setDraftEnd] = useState(toDateInputValue(props.value.end))
  const parsedStart = fromDateInputValue(draftStart)
  const parsedEnd = fromDateInputValue(draftEnd)
  const invalidRange = Boolean(
    parsedStart && parsedEnd && parsedStart.getTime() > parsedEnd.getTime()
  )

  const label = useMemo(() => {
    if (!props.value.start && !props.value.end) return t('Date Range')
    const startText = props.value.start
      ? dayjs(props.value.start).format('YYYY-MM-DD HH:mm')
      : '-'
    const endText = props.value.end
      ? dayjs(props.value.end).format('YYYY-MM-DD HH:mm')
      : '-'
    return `${startText} ~ ${endText}`
  }, [props.value.end, props.value.start, t])

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      setDraftStart(toDateInputValue(props.value.start))
      setDraftEnd(toDateInputValue(props.value.end))
    }
    setOpen(nextOpen)
  }

  const handleApply = () => {
    if (invalidRange) return
    props.onChange({ start: parsedStart, end: parsedEnd })
    setOpen(false)
  }

  const handlePreset = (days: number) => {
    const end = dayjs().endOf('day').toDate()
    const start = dayjs()
      .subtract(days - 1, 'day')
      .startOf('day')
      .toDate()
    setDraftStart(toDateInputValue(start))
    setDraftEnd(toDateInputValue(end))
    props.onChange({ start, end })
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            className={cn(
              'w-full justify-start gap-2 px-2.5 font-normal tabular-nums sm:w-[190px]',
              !props.value.start && !props.value.end && 'text-muted-foreground'
            )}
          />
        }
      >
        <CalendarDays className='size-4 shrink-0' />
        <span className='truncate'>{label}</span>
      </PopoverTrigger>
      <PopoverContent
        align='start'
        className='w-[min(520px,calc(100vw-2rem))] p-3'
      >
        <div className='space-y-3'>
          <div className='grid gap-2 sm:grid-cols-[1fr_auto_1fr] sm:items-end'>
            <label className='space-y-1.5'>
              <span className='text-muted-foreground block text-xs'>
                {t('Start Time')}
              </span>
              <Input
                type='datetime-local'
                value={draftStart}
                aria-invalid={invalidRange}
                onChange={(event) => setDraftStart(event.target.value)}
                className='tabular-nums'
              />
            </label>
            <span className='text-muted-foreground hidden pb-2 text-xs sm:block'>
              ~
            </span>
            <label className='space-y-1.5'>
              <span className='text-muted-foreground block text-xs'>
                {t('End Time')}
              </span>
              <Input
                type='datetime-local'
                value={draftEnd}
                aria-invalid={invalidRange}
                onChange={(event) => setDraftEnd(event.target.value)}
                className='tabular-nums'
              />
            </label>
          </div>

          {invalidRange ? (
            <p className='text-destructive text-xs'>
              {t('Start time must not be after end time')}
            </p>
          ) : null}

          <div className='flex flex-wrap gap-1.5'>
            <Button
              type='button'
              variant='secondary'
              size='sm'
              className='flex-1'
              onClick={() => handlePreset(1)}
            >
              {t('Today')}
            </Button>
            <Button
              type='button'
              variant='secondary'
              size='sm'
              className='flex-1'
              onClick={() => handlePreset(7)}
            >
              {t('7 Days')}
            </Button>
            <Button
              type='button'
              variant='secondary'
              size='sm'
              className='flex-1'
              onClick={() => handlePreset(30)}
            >
              {t('30 Days')}
            </Button>
          </div>

          <div className='flex justify-end gap-2 border-t pt-3'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => {
                setDraftStart('')
                setDraftEnd('')
              }}
            >
              {t('Clear')}
            </Button>
            <Button
              type='button'
              size='sm'
              disabled={invalidRange}
              onClick={handleApply}
            >
              {t('Confirm')}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}

export function ReferralCommissionDetails(
  props: ReferralCommissionDetailsProps
) {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const [paymentMethod, setPaymentMethod] = useState('all')
  const [dateRange, setDateRange] = useState<DateRange>({})
  const totalPages = Math.max(1, Math.ceil(props.total / props.pageSize))
  const pageNumbers = getPageNumbers(props.page, totalPages)
  const pageItems = pageNumbers.map((pageNumber, index) => ({
    key:
      pageNumber === '...'
        ? `ellipsis-${String(pageNumbers[index - 1])}-${String(pageNumbers[index + 1])}`
        : `page-${pageNumber}`,
    pageNumber,
  }))

  const paymentItems = useMemo(() => {
    const methods = new Map<string, string>()
    for (const method of props.paymentMethods) {
      if (method.type) {
        methods.set(
          method.type,
          method.name || getPaymentMethodName(method.type, t)
        )
      }
    }
    for (const record of props.records) {
      if (record.payment_method && !methods.has(record.payment_method)) {
        methods.set(
          record.payment_method,
          getPaymentMethodName(record.payment_method, t)
        )
      }
    }
    return [
      { label: t('All sources'), value: 'all' },
      ...[...methods].map(([value, label]) => ({ label, value })),
    ]
  }, [props.paymentMethods, props.records, t])

  const handleSearch = () => {
    const normalizedKeyword = keyword.trim().replaceAll('%', '')
    let keywordFilter: string | undefined
    if (normalizedKeyword) {
      const canUseFuzzySearch =
        normalizedKeyword.length > 1 ||
        (normalizedKeyword.codePointAt(0) ?? 0) > 127
      keywordFilter = canUseFuzzySearch
        ? `%${normalizedKeyword}%`
        : normalizedKeyword
    }
    props.onSearch({
      keyword: keywordFilter,
      payment_method: paymentMethod === 'all' ? undefined : paymentMethod,
      start_time: dateRange.start
        ? Math.floor(dateRange.start.getTime() / 1000)
        : undefined,
      end_time: dateRange.end
        ? Math.floor(dateRange.end.getTime() / 1000)
        : undefined,
    })
  }

  const handleSearchKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') handleSearch()
  }

  const handleReset = () => {
    setKeyword('')
    setPaymentMethod('all')
    setDateRange({})
    props.onSearch({})
  }

  let mobileRecords: ReactNode
  if (props.loading) {
    mobileRecords = (
      <div className='divide-y'>
        {LOADING_ROW_KEYS.map((rowKey) => (
          <div key={rowKey} className='space-y-3 p-3'>
            <div className='flex items-center justify-between gap-3'>
              <Skeleton className='h-4 w-28' />
              <Skeleton className='h-5 w-16' />
            </div>
            <div className='grid grid-cols-2 gap-3'>
              <Skeleton className='h-8 w-full' />
              <Skeleton className='h-8 w-full' />
              <Skeleton className='h-8 w-full' />
              <Skeleton className='h-8 w-full' />
            </div>
          </div>
        ))}
      </div>
    )
  } else if (props.records.length === 0) {
    mobileRecords = (
      <div className='text-muted-foreground flex h-40 flex-col items-center justify-center px-4 text-center'>
        <ReceiptText className='mb-2 size-8 opacity-40' />
        <p className='text-sm font-medium'>{t('No commission records')}</p>
        <p className='mt-1 text-xs'>
          {t('New commission records will appear here.')}
        </p>
      </div>
    )
  } else {
    mobileRecords = (
      <div className='divide-y'>
        {props.records.map((record) => {
          const paymentMethodLabel = getPaymentMethodName(
            record.payment_method,
            t
          )
          const isAvailable = record.status === 'available'
          return (
            <article key={record.id} className='space-y-3 p-3'>
              <div className='flex min-w-0 items-center justify-between gap-3'>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>{t('Member')}</p>
                  <p className='truncate font-medium'>
                    {record.invitee_name || record.invitee_id}
                  </p>
                </div>
                <StatusBadge
                  label={
                    isAvailable
                      ? t('Commission released')
                      : t('Commission frozen')
                  }
                  variant={isAvailable ? 'success' : 'warning'}
                  copyable={false}
                />
              </div>

              <div className='grid grid-cols-2 gap-x-4 gap-y-3'>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>{t('Source')}</p>
                  <p className='truncate text-sm'>{t('Online top-up')}</p>
                  <p className='text-muted-foreground truncate text-xs'>
                    {paymentMethodLabel}
                  </p>
                </div>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Top-up quota')}
                  </p>
                  <p className='truncate font-mono text-sm font-semibold'>
                    {formatQuota(record.base_quota)}
                  </p>
                </div>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Commission · Rate')}
                  </p>
                  <p className='truncate font-mono text-sm font-semibold'>
                    {formatQuota(record.reward_quota)} ·{' '}
                    {(record.rate_bps / 100).toFixed(2)}%
                  </p>
                </div>
                <div className='min-w-0'>
                  <p className='text-muted-foreground text-xs'>{t('Time')}</p>
                  <p className='text-muted-foreground truncate text-xs tabular-nums'>
                    {formatTimestamp(record.create_time)}
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
  } else if (props.records.length === 0) {
    tableRows = (
      <TableRow>
        <TableCell colSpan={6} className='h-40 text-center'>
          <div className='text-muted-foreground flex flex-col items-center'>
            <ReceiptText className='mb-2 size-8 opacity-40' />
            <p className='text-sm font-medium'>{t('No commission records')}</p>
            <p className='mt-1 text-xs'>
              {t('New commission records will appear here.')}
            </p>
          </div>
        </TableCell>
      </TableRow>
    )
  } else {
    tableRows = props.records.map((record) => {
      const paymentMethodLabel = getPaymentMethodName(record.payment_method, t)
      const isAvailable = record.status === 'available'
      return (
        <TableRow key={record.id}>
          <TableCell className='max-w-48 font-medium'>
            <span className='block truncate' title={record.invitee_name}>
              {record.invitee_name || record.invitee_id}
            </span>
          </TableCell>
          <TableCell>
            <div className='flex min-w-0 flex-col items-start gap-1'>
              <StatusBadge
                label={t('Online top-up')}
                variant='neutral'
                copyable={false}
                className='bg-muted/50 px-2'
              />
              <span className='text-muted-foreground max-w-36 truncate text-xs'>
                {paymentMethodLabel}
              </span>
            </div>
          </TableCell>
          <TableCell className='font-mono font-semibold'>
            {formatQuota(record.base_quota)}
          </TableCell>
          <TableCell>
            <div className='font-mono font-semibold'>
              {formatQuota(record.reward_quota)}
            </div>
            <div className='text-muted-foreground mt-0.5 text-xs'>
              {(record.rate_bps / 100).toFixed(2)}%
            </div>
          </TableCell>
          <TableCell className='text-muted-foreground'>
            {formatTimestamp(record.create_time)}
          </TableCell>
          <TableCell className='text-right'>
            <StatusBadge
              label={
                isAvailable ? t('Commission released') : t('Commission frozen')
              }
              variant={isAvailable ? 'success' : 'warning'}
              copyable={false}
              className='ml-auto'
            />
          </TableCell>
        </TableRow>
      )
    })
  }

  return (
    <Card data-card-hover='false' className='gap-0 py-0'>
      <CardHeader className='gap-1 border-b p-4 sm:p-5'>
        <CardTitle className='text-base font-semibold'>
          {t('Commission details')}
        </CardTitle>
        <CardDescription>
          {t('View every commission and its current settlement status.')}
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
              placeholder={t('Search member name')}
              aria-label={t('Search member name')}
              className='pl-9'
            />
          </div>

          <Select
            items={paymentItems}
            value={paymentMethod}
            onValueChange={(value) => value !== null && setPaymentMethod(value)}
          >
            <SelectTrigger className='w-full sm:w-40'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {paymentItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>

          <ReferralDateRangeFilter value={dateRange} onChange={setDateRange} />

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

        <div className='overflow-hidden rounded-lg border lg:hidden'>
          {mobileRecords}
        </div>

        <div className='hidden overflow-hidden rounded-lg border lg:block'>
          <Table className='min-w-[820px]'>
            <TableHeader className='bg-muted/40'>
              <TableRow className='hover:bg-transparent'>
                <TableHead>{t('Member')}</TableHead>
                <TableHead>{t('Source')}</TableHead>
                <TableHead>{t('Top-up quota')}</TableHead>
                <TableHead>{t('Commission · Rate')}</TableHead>
                <TableHead>{t('Time')}</TableHead>
                <TableHead className='text-right'>{t('Status')}</TableHead>
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
