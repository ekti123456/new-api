import { ArrowDown01Icon, ArrowRight01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  getPerformanceErrors,
  type PerformanceErrorQuery,
} from '@/features/dashboard/api'
import type { PerformanceErrorItem } from '@/features/dashboard/types'
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
import { formatNumber } from '@/lib/format'

import { PerformanceErrorRow } from './performance-error-row'

type PerformanceErrorGroupProps = {
  item: PerformanceErrorItem
  locale: string
  filters: PerformanceErrorQuery
}

function GroupDetails(props: PerformanceErrorGroupProps) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const params = {
    ...props.filters,
    grouped: false,
    errorGroupId: props.item.error_group_id,
    page,
    pageSize: 20,
  }
  const query = useQuery({
    queryKey: ['dashboard-performance-error-details', params],
    queryFn: () => getPerformanceErrors(params),
    enabled: Boolean(props.item.error_group_id),
    staleTime: 15 * 1000,
    retry: false,
  })
  const data = query.data?.data
  const totalPages = data
    ? Math.max(1, Math.ceil(data.total / data.page_size))
    : 1

  if (query.isError) {
    return (
      <div
        role='alert'
        className='text-destructive flex items-center gap-3 px-5 py-3 text-xs'
      >
        {t('Unable to load performance errors')}
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }
  if (query.isLoading) return <Skeleton className='m-4 h-12' />
  if (!data?.items.length) {
    return (
      <div className='text-muted-foreground px-5 py-3 text-xs'>
        {t('No performance errors in the selected period')}
      </div>
    )
  }
  return (
    <>
      {data.items.map((item) => (
        <PerformanceErrorRow
          key={item.id}
          item={item}
          locale={props.locale}
          detail
        />
      ))}
      {data.total > data.page_size && (
        <div className='flex items-center justify-end gap-2 px-5 py-3 text-xs'>
          <span>
            {t('Page {{current}} of {{total}}', {
              current: data.page,
              total: totalPages,
            })}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={query.isFetching || page <= 1}
            onClick={() => setPage((current) => current - 1)}
          >
            {t('Previous page')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={query.isFetching || page >= totalPages}
            onClick={() => setPage((current) => current + 1)}
          >
            {t('Next page')}
          </Button>
        </div>
      )}
    </>
  )
}

export function PerformanceErrorGroup(props: PerformanceErrorGroupProps) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const detailsID = useId()
  const count = props.item.occurrence_count ?? 1
  return (
    <div className='border-b last:border-b-0'>
      <PerformanceErrorRow
        item={props.item}
        locale={props.locale}
        trailing={
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-7 gap-1 px-2 font-mono tabular-nums'
            aria-expanded={expanded}
            aria-controls={expanded ? detailsID : undefined}
            aria-label={t(
              expanded
                ? 'Collapse {{count}} errors'
                : 'Expand {{count}} errors',
              { count }
            )}
            disabled={!props.item.error_group_id}
            onClick={() => setExpanded((current) => !current)}
          >
            {expanded ? (
              <HugeiconsIcon
                icon={ArrowDown01Icon}
                className='size-3'
                aria-hidden='true'
              />
            ) : (
              <HugeiconsIcon
                icon={ArrowRight01Icon}
                className='size-3'
                aria-hidden='true'
              />
            )}
            {formatNumber(count, props.locale)}
          </Button>
        }
      />
      {expanded && (
        <div id={detailsID} role='region' aria-label={t('Error details')}>
          <GroupDetails {...props} />
        </div>
      )}
    </div>
  )
}
