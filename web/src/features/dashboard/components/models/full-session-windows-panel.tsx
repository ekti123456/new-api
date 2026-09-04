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
import { CircleAlert, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { UserInfoDialog } from '@/features/usage-logs/components/dialogs/user-info-dialog'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatSessionWindowCountdown,
  getSessionWindowRemainingSeconds,
} from '@/lib/format'
import { cn } from '@/lib/utils'

import { getFullSessionWindows } from '../../api'

function formatWindowDuration(
  seconds: number,
  t: (key: string, options?: Record<string, unknown>) => string
) {
  if (seconds >= 86400 && seconds % 86400 === 0) {
    return t('{{count}} days', { count: seconds / 86400 })
  }
  if (seconds >= 3600 && seconds % 3600 === 0) {
    return t('{{count}} hours', { count: seconds / 3600 })
  }
  if (seconds >= 60 && seconds % 60 === 0) {
    return t('{{count}} minutes', { count: seconds / 60 })
  }
  return `${seconds}s`
}

export function FullSessionWindowsPanel() {
  const { t, i18n } = useTranslation()
  const [selectedUserId, setSelectedUserId] = useState<number | null>(null)
  const [userDialogOpen, setUserDialogOpen] = useState(false)
  const [nowMs, setNowMs] = useState(() => Date.now())
  const query = useQuery({
    queryKey: ['dashboard-full-session-windows'],
    queryFn: getFullSessionWindows,
    staleTime: 10 * 1000,
    refetchInterval: 30 * 1000,
    retry: false,
  })
  const data = query.data?.data
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])

  const openUser = (userId: number) => {
    setSelectedUserId(userId)
    setUserDialogOpen(true)
  }

  return (
    <>
      <div className='overflow-hidden rounded-lg border'>
        <div className='flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3 sm:px-5'>
          <div className='flex min-w-0 items-center gap-2'>
            <IconBadge tone='destructive' size='sm'>
              <CircleAlert />
            </IconBadge>
            <div className='min-w-0'>
              <div className='truncate text-sm font-semibold'>
                {t('Users with full session windows')}
              </div>
              <div className='text-muted-foreground text-xs'>
                {t(
                  'A user is listed when any Codex2API target reports that its session window is full.'
                )}
              </div>
            </div>
          </div>
          <div className='flex items-center gap-2'>
            <span className='text-muted-foreground text-xs tabular-nums'>
              {t('Users')}:{' '}
              {(data?.full_user_count ?? 0).toLocaleString(locale)}
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

        <div className='max-h-96 overflow-y-auto px-4 py-3 sm:px-5'>
          {query.isLoading &&
            ['session-full-1', 'session-full-2', 'session-full-3'].map(
              (key) => (
                <div
                  key={key}
                  className='border-border/60 space-y-2 border-b py-3 last:border-0'
                >
                  <div className='flex justify-between gap-3'>
                    <Skeleton className='h-4 w-32' />
                    <Skeleton className='h-5 w-14 rounded-full' />
                  </div>
                  <Skeleton className='h-3.5 w-48' />
                </div>
              )
            )}

          {!query.isLoading && query.isError && (
            <div className='text-destructive py-6 text-center text-xs'>
              {t('Unable to load full session windows')}
            </div>
          )}

          {!query.isLoading &&
            !query.isError &&
            (!data || data.items.length === 0) && (
              <div className='text-muted-foreground py-6 text-center text-xs'>
                {t('No users currently have a full session window')}
              </div>
            )}

          {!query.isLoading &&
            !query.isError &&
            data?.items.map((item) => {
              const name =
                item.display_name?.trim() ||
                item.username?.trim() ||
                `#${item.user_id}`
              return (
                <div
                  key={item.user_id}
                  className='border-border/60 border-b py-3 last:border-0'
                >
                  <div className='flex min-w-0 items-center justify-between gap-3'>
                    <button
                      type='button'
                      className='min-w-0 truncate text-left text-sm font-semibold hover:underline'
                      title={name}
                      onClick={() => openUser(item.user_id)}
                    >
                      {name}
                      <span className='text-muted-foreground ml-1.5 font-mono text-xs font-normal'>
                        #{item.user_id}
                      </span>
                    </button>
                    <span className='bg-destructive/10 text-destructive shrink-0 rounded-full px-2 py-0.5 text-[11px] font-semibold tabular-nums'>
                      {t('{{count}} full targets', {
                        count: item.full_target_count,
                      })}
                    </span>
                  </div>
                  <div className='mt-2 space-y-1.5'>
                    {item.targets.map((target) => {
                      const remaining = getSessionWindowRemainingSeconds(
                        target.session_window_updated_at,
                        target.session_window_seconds,
                        nowMs
                      )
                      let resetLabel = '-'
                      if (remaining === 0) {
                        resetLabel = t('Available')
                      } else if (remaining !== null) {
                        resetLabel = formatSessionWindowCountdown(remaining)
                      }
                      return (
                        <div
                          key={target.target}
                          className='bg-muted/50 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 rounded-md px-2.5 py-1.5 text-xs'
                        >
                          <span className='text-destructive font-mono font-semibold tabular-nums'>
                            {target.session_window_used} /{' '}
                            {target.session_window_limit}
                          </span>
                          <span className='text-muted-foreground'>
                            {formatWindowDuration(
                              target.session_window_seconds,
                              t
                            )}
                          </span>
                          <span className='text-amber-600 dark:text-amber-400 shrink-0 tabular-nums'>
                            {t('Resets in:')} {resetLabel}
                          </span>
                          <span
                            className='min-w-0 flex-1 truncate font-mono'
                            title={target.target}
                          >
                            {target.target}
                          </span>
                          <span className='text-muted-foreground shrink-0 tabular-nums'>
                            {new Date(
                              target.session_window_updated_at
                            ).toLocaleString(locale)}
                          </span>
                        </div>
                      )
                    })}
                  </div>
                </div>
              )
            })}
        </div>
      </div>

      <UserInfoDialog
        userId={selectedUserId}
        open={userDialogOpen}
        onOpenChange={setUserDialogOpen}
      />
    </>
  )
}
