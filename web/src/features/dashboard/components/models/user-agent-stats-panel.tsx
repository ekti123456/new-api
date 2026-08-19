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
import { MonitorSmartphone } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserAgentStats } from '@/features/dashboard/api'
import { buildQueryParams, getDefaultDays } from '@/features/dashboard/lib'
import type { DashboardFilters } from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { computeTimeRange } from '@/lib/time'

interface UserAgentStatsPanelProps {
  filters?: DashboardFilters
}

export function UserAgentStatsPanel(props: UserAgentStatsPanelProps) {
  const { t, i18n } = useTranslation()
  const queryParams = useMemo(() => {
    const timeRange = computeTimeRange(
      getDefaultDays(props.filters?.time_granularity),
      props.filters?.start_timestamp,
      props.filters?.end_timestamp
    )
    const params = buildQueryParams(timeRange, props.filters)
    return {
      start_timestamp: params.start_timestamp,
      end_timestamp: params.end_timestamp,
      ...(params.username && { username: params.username }),
    }
  }, [props.filters])
  const statsQuery = useQuery({
    queryKey: ['dashboard-user-agent-stats', queryParams],
    queryFn: () => getUserAgentStats(queryParams),
    staleTime: 60 * 1000,
    retry: false,
  })
  const stats = statsQuery.data?.data
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3 sm:px-5'>
        <div className='flex min-w-0 items-center gap-2'>
          <IconBadge tone='chart-2' size='sm'>
            <MonitorSmartphone />
          </IconBadge>
          <div className='min-w-0'>
            <div className='truncate text-sm font-semibold'>
              {t('Request clients (User-Agent)')}
            </div>
            <div className='text-muted-foreground text-xs'>
              {t(
                'Grouped by client family; clients below 5% are combined as Other.'
              )}
            </div>
          </div>
        </div>
        <div className='text-muted-foreground text-xs tabular-nums'>
          {t('Requests')}: {(stats?.total ?? 0).toLocaleString(locale)}
        </div>
      </div>

      <div className='space-y-3 px-4 py-4 sm:px-5'>
        {statsQuery.isLoading &&
          ['ua-1', 'ua-2', 'ua-3'].map((key) => (
            <div key={key} className='space-y-1.5'>
              <div className='flex justify-between gap-3'>
                <Skeleton className='h-4 w-32' />
                <Skeleton className='h-4 w-20' />
              </div>
              <Skeleton className='h-2 w-full rounded-full' />
            </div>
          ))}

        {!statsQuery.isLoading && (!stats || stats.items.length === 0) && (
          <div className='text-muted-foreground py-5 text-center text-xs'>
            {t('No data available')}
          </div>
        )}

        {!statsQuery.isLoading &&
          stats?.items.map((item) => {
            let clientName = item.client_family
            if (item.is_other) clientName = t('Other')
            if (item.client_family === 'Unknown') clientName = t('Unknown')
            const percentage = Math.max(0, Math.min(100, item.percentage))

            return (
              <div key={item.client_family} className='space-y-1.5'>
                <div className='flex min-w-0 items-center justify-between gap-3 text-xs'>
                  <span className='truncate font-medium' title={clientName}>
                    {clientName}
                  </span>
                  <span className='text-muted-foreground shrink-0 font-mono tabular-nums'>
                    {item.count.toLocaleString(locale)} ·{' '}
                    {percentage.toFixed(1)}%
                  </span>
                </div>
                <div
                  className='bg-muted h-2 overflow-hidden rounded-full'
                  role='progressbar'
                  aria-label={clientName}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={percentage}
                >
                  <div
                    className='bg-chart-2 h-full rounded-full transition-[width]'
                    style={{ width: `${percentage}%` }}
                  />
                </div>
              </div>
            )
          })}
      </div>
    </div>
  )
}
