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
  AlertTriangle,
  BellRing,
  Loader2,
  Mail,
  MapPin,
  RefreshCw,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getLogIPLocation, type LogIPLocation } from '@/features/usage-logs/api'
import { UserInfoDialog } from '@/features/usage-logs/components/dialogs/user-info-dialog'
import { toIntlLocale } from '@/i18n/languages'
import { formatUseTime } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  getUserPerformanceAnomalies,
  type UserPerformanceAlertChannel,
} from '../../api'
import type { UserPerformanceAnomalyItem } from '../../types'
import { UserPerformanceContactDialog } from './user-performance-contact-dialog'

type UserPerformanceAnomaliesPanelProps = {
  username?: string
}

export function UserPerformanceAnomaliesPanel(
  props: UserPerformanceAnomaliesPanelProps
) {
  const { t, i18n } = useTranslation()
  const [locations, setLocations] = useState<Record<string, LogIPLocation>>({})
  const [loadingLocationKey, setLoadingLocationKey] = useState<string | null>(
    null
  )
  const [selectedUserId, setSelectedUserId] = useState<number | null>(null)
  const [userDialogOpen, setUserDialogOpen] = useState(false)
  const [contactItem, setContactItem] =
    useState<UserPerformanceAnomalyItem | null>(null)
  const [contactChannel, setContactChannel] =
    useState<UserPerformanceAlertChannel | null>(null)
  const [contactDialogOpen, setContactDialogOpen] = useState(false)
  const [page, setPage] = useState(1)
  const pageSize = 20

  useEffect(() => {
    setPage(1)
  }, [props.username])

  const query = useQuery({
    queryKey: [
      'dashboard-user-performance-anomalies',
      props.username,
      page,
      pageSize,
    ],
    queryFn: () => getUserPerformanceAnomalies(props.username, page, pageSize),
    staleTime: 30 * 1000,
    refetchInterval: 60 * 1000,
    retry: false,
  })
  const data = query.data?.data
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const totalPages = data
    ? Math.max(1, Math.ceil(data.total / data.page_size))
    : 1

  useEffect(() => {
    if (data && data.page !== page) {
      setPage(data.page)
    }
  }, [data, page])

  const refreshLocation = async (item: UserPerformanceAnomalyItem) => {
    if (!item.ip) return
    const key = `${item.user_id}:${item.group}`
    setLoadingLocationKey(key)
    try {
      const response = await getLogIPLocation(
        item.ip,
        i18n.resolvedLanguage || i18n.language
      )
      const locationData = response.data
      if (!response.success || !locationData) {
        throw new Error(response.message || t('Failed to query IP location'))
      }
      setLocations((current) => ({ ...current, [key]: locationData }))
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to query IP location')
      )
    } finally {
      setLoadingLocationKey(null)
    }
  }

  const openUser = (userId: number) => {
    setSelectedUserId(userId)
    setUserDialogOpen(true)
  }

  const openContact = (
    item: UserPerformanceAnomalyItem,
    channel: UserPerformanceAlertChannel
  ) => {
    setContactItem(item)
    setContactChannel(channel)
    setContactDialogOpen(true)
  }

  return (
    <>
      <div className='overflow-hidden rounded-lg border'>
        <div className='flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3 sm:px-5'>
          <div className='flex min-w-0 items-center gap-2'>
            <IconBadge tone='warning' size='sm'>
              <AlertTriangle />
            </IconBadge>
            <div className='min-w-0'>
              <div className='truncate text-sm font-semibold'>
                {t('User performance anomalies')}
              </div>
              <div className='text-muted-foreground text-xs'>
                {t(
                  'Root-only analysis of the last two hours for monitored groups.'
                )}
              </div>
              {data && (
                <div className='text-muted-foreground mt-0.5 text-[11px] tabular-nums'>
                  {t('Requests with first token above group average')}: ≥
                  {data.ttft_above_average_threshold}% · {t('Error rate')}: &gt;
                  {data.error_rate_threshold}% · n≥{data.min_requests}
                </div>
              )}
            </div>
          </div>
          <div className='flex items-center gap-2'>
            <span className='text-muted-foreground text-xs tabular-nums'>
              {t('Users')}: {(data?.total ?? 0).toLocaleString(locale)}
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

        <div className='max-h-[32rem] overflow-auto'>
          {query.isLoading && (
            <div className='space-y-3 px-4 py-4 sm:px-5'>
              {['anomaly-1', 'anomaly-2', 'anomaly-3'].map((key) => (
                <Skeleton key={key} className='h-16 w-full' />
              ))}
            </div>
          )}

          {!query.isLoading && query.isError && (
            <div className='text-destructive px-4 py-8 text-center text-xs'>
              {t('Unable to load user performance anomalies')}
            </div>
          )}

          {!query.isLoading &&
            !query.isError &&
            data?.monitored_groups.length === 0 && (
              <div className='text-muted-foreground px-4 py-8 text-center text-xs'>
                {t(
                  'Select monitored groups in system settings to enable this analysis.'
                )}
              </div>
            )}

          {!query.isLoading &&
            !query.isError &&
            data &&
            data.monitored_groups.length > 0 &&
            data.items.length === 0 && (
              <div className='text-muted-foreground px-4 py-8 text-center text-xs'>
                {t('No user performance anomalies in the last two hours')}
              </div>
            )}

          {!query.isLoading && !query.isError && data?.items.length ? (
            <div className='min-w-[70rem] divide-y'>
              <div className='text-muted-foreground bg-muted/30 grid grid-cols-[minmax(10rem,1.1fr)_8rem_13rem_9rem_minmax(12rem,1.4fr)_minmax(12rem,1.2fr)_6rem] gap-3 px-5 py-2 text-xs font-medium'>
                <span>{t('User')}</span>
                <span>{t('Group')}</span>
                <span>{t('First token')}</span>
                <span>{t('Error rate')}</span>
                <span>{t('Access URL')}</span>
                <span>{t('IP Address')}</span>
                <span>{t('Actions')}</span>
              </div>
              {data.items.map((item) => {
                const key = `${item.user_id}:${item.group}`
                const location = locations[key]
                const loadingLocation = loadingLocationKey === key
                const username = item.username || `#${item.user_id}`
                return (
                  <div
                    key={key}
                    className='grid grid-cols-[minmax(10rem,1.1fr)_8rem_13rem_9rem_minmax(12rem,1.4fr)_minmax(12rem,1.2fr)_6rem] gap-3 px-5 py-3 text-xs'
                  >
                    <div className='min-w-0'>
                      <button
                        type='button'
                        className='block max-w-full truncate text-left font-semibold hover:underline'
                        title={username}
                        onClick={() => openUser(item.user_id)}
                      >
                        {username}
                      </button>
                      <span className='text-muted-foreground font-mono'>
                        #{item.user_id} ·{' '}
                        {item.request_count.toLocaleString(locale)}{' '}
                        {t('requests')}
                      </span>
                    </div>
                    <span className='w-fit self-start rounded-full bg-sky-500/10 px-2 py-0.5 font-mono font-semibold text-sky-600 dark:text-sky-400'>
                      {item.group}
                    </span>
                    <div className='min-w-0'>
                      <span
                        className={cn(
                          'block font-mono font-semibold tabular-nums',
                          item.ttft_anomaly
                            ? 'text-amber-600 dark:text-amber-400'
                            : 'text-muted-foreground'
                        )}
                      >
                        {item.ttft_count > 0
                          ? t(
                              '{{percentage}}% of requests above group average',
                              {
                                percentage:
                                  item.above_group_avg_percentage.toFixed(1),
                              }
                            )
                          : '—'}
                      </span>
                      {item.ttft_count > 0 && (
                        <span className='text-muted-foreground block tabular-nums'>
                          {formatUseTime(item.avg_ttft_ms / 1000)} /{' '}
                          {formatUseTime(item.group_avg_ttft_ms / 1000)} · n=
                          {item.ttft_count}
                        </span>
                      )}
                    </div>
                    <div>
                      <span
                        className={cn(
                          'font-mono font-semibold tabular-nums',
                          item.error_anomaly
                            ? 'text-rose-600 dark:text-rose-400'
                            : 'text-muted-foreground'
                        )}
                      >
                        {item.error_rate.toFixed(1)}%
                      </span>
                      <span className='text-muted-foreground block tabular-nums'>
                        {item.error_count}/{item.request_count}
                      </span>
                    </div>
                    <span
                      className='min-w-0 truncate font-mono'
                      title={item.access_url || undefined}
                    >
                      {item.access_url || '—'}
                    </span>
                    <div className='min-w-0'>
                      <div className='flex min-w-0 items-center gap-1.5'>
                        <span className='truncate font-mono' title={item.ip}>
                          {item.ip || '—'}
                        </span>
                        {item.ip && (
                          <button
                            type='button'
                            className='text-muted-foreground hover:bg-muted hover:text-foreground inline-flex size-6 shrink-0 items-center justify-center rounded transition-colors disabled:opacity-50'
                            aria-label={t('Refresh IP location')}
                            title={t('Refresh IP location')}
                            disabled={loadingLocation}
                            onClick={() => void refreshLocation(item)}
                          >
                            {loadingLocation ? (
                              <Loader2 className='size-3 animate-spin' />
                            ) : (
                              <MapPin className='size-3' />
                            )}
                          </button>
                        )}
                      </div>
                      {location && (
                        <span className='text-muted-foreground block truncate'>
                          {location.location}
                          {location.isp ? ` · ${location.isp}` : ''}
                        </span>
                      )}
                      {item.last_seen_at > 0 && (
                        <span className='text-muted-foreground block tabular-nums'>
                          {new Date(item.last_seen_at * 1000).toLocaleString(
                            locale
                          )}
                        </span>
                      )}
                    </div>
                    <div className='flex items-start gap-1'>
                      <button
                        type='button'
                        className='text-muted-foreground inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-sky-500/10 hover:text-sky-600 dark:hover:text-sky-400'
                        aria-label={t('Send personal notification')}
                        title={t('Send personal notification')}
                        onClick={() => openContact(item, 'in_app')}
                      >
                        <BellRing className='size-3.5' />
                      </button>
                      <button
                        type='button'
                        className='text-muted-foreground inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-emerald-500/10 hover:text-emerald-600 disabled:cursor-not-allowed disabled:opacity-30 dark:hover:text-emerald-400'
                        aria-label={
                          item.email?.trim()
                            ? t('Send email')
                            : t('User has no email address')
                        }
                        title={
                          item.email?.trim()
                            ? `${t('Send email')} · ${item.email}`
                            : t('User has no email address')
                        }
                        disabled={!item.email?.trim()}
                        onClick={() => openContact(item, 'email')}
                      >
                        <Mail className='size-3.5' />
                      </button>
                    </div>
                  </div>
                )
              })}
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

      <UserInfoDialog
        userId={selectedUserId}
        open={userDialogOpen}
        onOpenChange={setUserDialogOpen}
      />
      <UserPerformanceContactDialog
        item={contactItem}
        channel={contactChannel}
        open={contactDialogOpen}
        onOpenChange={setContactDialogOpen}
      />
    </>
  )
}
