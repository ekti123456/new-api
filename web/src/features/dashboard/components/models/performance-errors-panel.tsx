import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  Filter,
  RefreshCw,
  RotateCcw,
  Search,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { getPerformanceErrors } from '@/features/dashboard/api'
import type {
  DashboardFilters,
  PerformanceErrorItem,
} from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'

type PerformanceErrorsPanelProps = {
  filters?: DashboardFilters
}

const PAGE_SIZE = 20

type ErrorFilters = {
  modelName: string
  username: string
  group: string
  errorType: string
  errorCode: string
  statusCode: string
}

function emptyErrorFilters(username = ''): ErrorFilters {
  return {
    modelName: '',
    username,
    group: '',
    errorType: '',
    errorCode: '',
    statusCode: '',
  }
}

function displayValue(value: string | number | undefined) {
  if (value === undefined || value === null || value === '') return '—'
  return String(value)
}

function statusClass(statusCode: number) {
  if (statusCode >= 500) return 'text-rose-600 dark:text-rose-400'
  if (statusCode >= 400) return 'text-amber-600 dark:text-amber-400'
  return 'text-muted-foreground'
}

function ErrorRow(props: { item: PerformanceErrorItem; locale: string }) {
  const { t } = useTranslation()
  const item = props.item
  const timestamp = item.created_at
    ? new Date(item.created_at * 1000).toLocaleString(props.locale)
    : '—'
  const user = item.username?.trim() || `#${item.user_id}`

  return (
    <div className='grid min-w-[86rem] grid-cols-[10rem_minmax(9rem,1fr)_minmax(9rem,1fr)_5rem_11rem_minmax(18rem,2fr)_minmax(11rem,1fr)_minmax(9rem,1fr)] gap-3 border-b px-5 py-3 text-xs last:border-b-0'>
      <span className='text-muted-foreground tabular-nums'>{timestamp}</span>
      <span className='truncate font-medium' title={user}>
        {user}
        <span className='text-muted-foreground ml-1 font-mono'>
          #{item.user_id}
        </span>
      </span>
      <span className='truncate font-mono' title={item.model_name}>
        {displayValue(item.model_name)}
      </span>
      <span
        className={cn(
          'font-mono font-semibold tabular-nums',
          statusClass(item.status_code)
        )}
      >
        {item.status_code || '—'}
      </span>
      <span className='min-w-0 truncate' title={item.error_type}>
        <span className='block truncate'>{displayValue(item.error_type)}</span>
        <span className='text-muted-foreground block truncate font-mono'>
          {displayValue(item.error_code)}
        </span>
      </span>
      <span
        className='text-muted-foreground min-w-0 truncate font-mono'
        title={item.error_reason}
      >
        {displayValue(item.error_reason)}
      </span>
      <span
        className='text-muted-foreground min-w-0 truncate font-mono'
        title={item.request_id || undefined}
      >
        {displayValue(item.request_id)}
      </span>
      <span className='min-w-0 truncate' title={item.channel_name || undefined}>
        <span className='block truncate'>
          {item.channel_name ||
            (item.channel_id ? `#${item.channel_id}` : t('Unknown'))}
        </span>
        <span className='text-muted-foreground block truncate font-mono'>
          {displayValue(item.group)}
        </span>
      </span>
    </div>
  )
}

export function PerformanceErrorsPanel(props: PerformanceErrorsPanelProps) {
  const { t, i18n } = useTranslation()
  const [page, setPage] = useState(1)
  const [filterOpen, setFilterOpen] = useState(false)
  const [errorFilters, setErrorFilters] = useState<ErrorFilters>(() =>
    emptyErrorFilters(props.filters?.username)
  )
  const [draftFilters, setDraftFilters] = useState<ErrorFilters>(() =>
    emptyErrorFilters(props.filters?.username)
  )
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) || 'en-US'
  const timeRange = useMemo(
    () =>
      computeTimeRange(
        1,
        props.filters?.start_timestamp,
        props.filters?.end_timestamp
      ),
    [props.filters?.end_timestamp, props.filters?.start_timestamp]
  )

  useEffect(() => {
    setErrorFilters((current) => ({
      ...current,
      username: props.filters?.username ?? '',
    }))
  }, [props.filters?.username])

  useEffect(() => {
    setPage(1)
  }, [
    errorFilters.errorCode,
    errorFilters.errorType,
    errorFilters.group,
    errorFilters.modelName,
    errorFilters.statusCode,
    errorFilters.username,
    props.filters?.end_timestamp,
    props.filters?.start_timestamp,
  ])

  const statusCode = Number.parseInt(errorFilters.statusCode, 10)
  const hasErrorFilters = Object.values(errorFilters).some(
    (value) => value.trim() !== ''
  )

  const query = useQuery({
    queryKey: [
      'dashboard-performance-errors',
      timeRange.start_timestamp,
      timeRange.end_timestamp,
      errorFilters.errorCode,
      errorFilters.errorType,
      errorFilters.group,
      errorFilters.modelName,
      errorFilters.statusCode,
      errorFilters.username,
      page,
    ],
    queryFn: () =>
      getPerformanceErrors({
        startTimestamp: timeRange.start_timestamp,
        endTimestamp: timeRange.end_timestamp,
        username: errorFilters.username || undefined,
        errorType: errorFilters.errorType || undefined,
        errorCode: errorFilters.errorCode || undefined,
        group: errorFilters.group || undefined,
        modelName: errorFilters.modelName || undefined,
        statusCode:
          Number.isFinite(statusCode) && statusCode > 0
            ? statusCode
            : undefined,
        page,
        pageSize: PAGE_SIZE,
      }),
    staleTime: 15 * 1000,
    refetchInterval: 30 * 1000,
    retry: false,
  })
  const data = query.data?.data
  const totalPages = data
    ? Math.max(1, Math.ceil(data.total / data.page_size))
    : 1

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3 sm:px-5'>
        <div className='flex min-w-0 items-center gap-2'>
          <IconBadge tone='destructive' size='sm'>
            <AlertTriangle />
          </IconBadge>
          <div className='min-w-0'>
            <div className='truncate text-sm font-semibold'>
              {t('Performance errors')}
            </div>
            <div className='text-muted-foreground text-xs'>
              {t('Final failed requests counted by model performance metrics')}
            </div>
          </div>
        </div>
        <div className='flex items-center gap-2'>
          <Dialog
            open={filterOpen}
            onOpenChange={setFilterOpen}
            title={t('Error filters')}
            description={t(
              'Filter final performance errors by request metadata'
            )}
            contentClassName='sm:max-w-lg'
            footer={
              <>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => {
                    const next = emptyErrorFilters(props.filters?.username)
                    setDraftFilters(next)
                    setErrorFilters(next)
                    setFilterOpen(false)
                  }}
                >
                  <RotateCcw className='mr-2 size-3.5' />
                  {t('Reset')}
                </Button>
                <Button
                  type='button'
                  onClick={() => {
                    setErrorFilters({ ...draftFilters })
                    setFilterOpen(false)
                  }}
                >
                  <Search className='mr-2 size-3.5' />
                  {t('Apply Filters')}
                </Button>
              </>
            }
            trigger={
              <Button
                type='button'
                variant={hasErrorFilters ? 'default' : 'outline'}
                size='sm'
                onClick={() => setDraftFilters({ ...errorFilters })}
              >
                <Filter className='mr-2 size-3.5' />
                {t('Filter')}
              </Button>
            }
          >
            <div className='grid gap-3 py-2 sm:grid-cols-2'>
              <div className='grid gap-1.5'>
                <Label htmlFor='performance-error-model'>{t('Model')}</Label>
                <Input
                  id='performance-error-model'
                  value={draftFilters.modelName}
                  placeholder={t('Model name')}
                  onChange={(event) =>
                    setDraftFilters((current) => ({
                      ...current,
                      modelName: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='performance-error-user'>{t('User')}</Label>
                <Input
                  id='performance-error-user'
                  value={draftFilters.username}
                  placeholder={t('Username')}
                  onChange={(event) =>
                    setDraftFilters((current) => ({
                      ...current,
                      username: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='performance-error-group'>{t('Group')}</Label>
                <Input
                  id='performance-error-group'
                  value={draftFilters.group}
                  placeholder={t('Group')}
                  onChange={(event) =>
                    setDraftFilters((current) => ({
                      ...current,
                      group: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='performance-error-status'>
                  {t('Status code')}
                </Label>
                <Input
                  id='performance-error-status'
                  type='number'
                  min='100'
                  max='599'
                  value={draftFilters.statusCode}
                  placeholder='503'
                  onChange={(event) =>
                    setDraftFilters((current) => ({
                      ...current,
                      statusCode: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='performance-error-type'>
                  {t('Error type')}
                </Label>
                <Input
                  id='performance-error-type'
                  value={draftFilters.errorType}
                  placeholder={t('Error type')}
                  onChange={(event) =>
                    setDraftFilters((current) => ({
                      ...current,
                      errorType: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='performance-error-code'>
                  {t('Error code')}
                </Label>
                <Input
                  id='performance-error-code'
                  value={draftFilters.errorCode}
                  placeholder={t('Error code')}
                  onChange={(event) =>
                    setDraftFilters((current) => ({
                      ...current,
                      errorCode: event.target.value,
                    }))
                  }
                />
              </div>
            </div>
          </Dialog>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {t('Total')}: {(data?.total ?? 0).toLocaleString(locale)}
          </span>
          <button
            type='button'
            className='text-muted-foreground hover:bg-muted hover:text-foreground inline-flex size-7 items-center justify-center rounded-md transition-colors disabled:opacity-50'
            aria-label={t('Refresh')}
            title={t('Refresh')}
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            <RefreshCw
              className={cn('size-3.5', query.isFetching && 'animate-spin')}
            />
          </button>
        </div>
      </div>

      <div className='max-h-[34rem] overflow-auto'>
        {query.isLoading && (
          <div className='space-y-3 px-4 py-4 sm:px-5'>
            {['error-1', 'error-2', 'error-3'].map((key) => (
              <Skeleton key={key} className='h-12 w-full' />
            ))}
          </div>
        )}
        {!query.isLoading && query.isError && (
          <div className='text-destructive px-4 py-8 text-center text-xs'>
            {t('Unable to load performance errors')}
          </div>
        )}
        {!query.isLoading && !query.isError && data?.items.length === 0 && (
          <div className='text-muted-foreground px-4 py-8 text-center text-xs'>
            {t('No performance errors in the selected period')}
          </div>
        )}
        {!query.isLoading && !query.isError && data?.items.length ? (
          <div>
            <div className='text-muted-foreground bg-muted/30 grid min-w-[86rem] grid-cols-[10rem_minmax(9rem,1fr)_minmax(9rem,1fr)_5rem_11rem_minmax(18rem,2fr)_minmax(11rem,1fr)_minmax(9rem,1fr)] gap-3 px-5 py-2 text-xs font-medium'>
              <span>{t('Time')}</span>
              <span>{t('User')}</span>
              <span>{t('Model')}</span>
              <span>{t('Status')}</span>
              <span>{t('Error type')}</span>
              <span>{t('Reason')}</span>
              <span>{t('Request ID')}</span>
              <span>{t('Channel')}</span>
            </div>
            {data.items.map((item) => (
              <ErrorRow key={item.id} item={item} locale={locale} />
            ))}
          </div>
        ) : null}
      </div>

      {data && data.total > data.page_size ? (
        <div className='flex items-center justify-between gap-3 border-t px-4 py-2.5 sm:px-5'>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {t('Page {{current}} of {{total}}', {
              current: data.page,
              total: totalPages,
            })}
          </span>
          <div className='flex items-center gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={query.isFetching || data.page <= 1}
              onClick={() => setPage((current) => Math.max(1, current - 1))}
            >
              {t('Previous page')}
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={query.isFetching || data.page >= totalPages}
              onClick={() =>
                setPage((current) => Math.min(totalPages, current + 1))
              }
            >
              {t('Next page')}
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  )
}
