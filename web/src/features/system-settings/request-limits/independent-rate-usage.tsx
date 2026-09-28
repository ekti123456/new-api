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
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePagination } from '@/components/data-table'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useDebounce } from '@/hooks/use-debounce'
import { formatNumber } from '@/lib/format'

import { getIndependentUsage } from './independent-rate-api'

export function IndependentRateUsage(props: { ruleID: string; name: string }) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const keyword = useDebounce(search, 300)
  const query = useQuery({
    queryKey: [
      'independent-rpm-usage',
      props.ruleID,
      keyword,
      pagination.pageIndex,
    ],
    queryFn: () =>
      getIndependentUsage(props.ruleID, keyword, pagination.pageIndex + 1),
    refetchInterval: 5000,
    retry: false,
  })
  const table = useReactTable({
    data: query.data?.items ?? [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    rowCount: query.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
  })
  return (
    <section aria-label={t('Recent request usage')} className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h3 className='font-medium'>
          {props.name} · {t('Last 60 seconds')}
        </h3>
        <Button
          variant='outline'
          onClick={() => query.refetch()}
          disabled={query.isFetching}
        >
          {t('Refresh')}
        </Button>
      </div>
      <Input
        aria-label={t('Filter active users')}
        placeholder={t('Search users by ID or name')}
        value={search}
        onChange={(event) => {
          setSearch(event.target.value)
          setPagination({ pageIndex: 0, pageSize: 20 })
        }}
      />
      {query.isError && <p role='alert'>{t('Unable to load rate usage')}</p>}
      <StaticDataTable
        data={query.data?.items ?? []}
        getRowKey={(row) => row.user_id}
        emptyContent={
          query.isFetching
            ? t('Loading...')
            : t('No requests in the last 60 seconds')
        }
        columns={[
          {
            id: 'user',
            header: t('User'),
            cell: (row) => `${row.username} (#${row.user_id})`,
          },
          {
            id: 'count',
            header: t('Requests / limit'),
            cell: (row) => (
              <span className='tabular-nums'>
                {formatNumber(row.count)} / {formatNumber(row.limit)}
              </span>
            ),
          },
          {
            id: 'state',
            header: t('Status'),
            cell: (row) => {
              if (!query.data?.enabled) return t('Disabled')
              return row.allowed ? t('Available') : t('Rate limited')
            },
          },
        ]}
      />
      <DataTablePagination table={table} compact />
      <p className='text-muted-foreground text-xs'>
        {t(
          'Counts admitted requests in a rolling minute, not concurrent requests. Refreshes every 5 seconds.'
        )}{' '}
        {query.data?.backend === 'memory' &&
          t('Memory counters are local to this instance.')}
      </p>
    </section>
  )
}
