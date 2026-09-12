import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { PerformanceErrorItem } from '@/features/dashboard/types'
import { cn } from '@/lib/utils'

function displayValue(value: string | number | undefined) {
  if (value === undefined || value === null || value === '') return '—'
  return String(value)
}

function statusClass(statusCode: number) {
  if (statusCode >= 500) return 'text-rose-600 dark:text-rose-400'
  if (statusCode >= 400) return 'text-amber-600 dark:text-amber-400'
  return 'text-muted-foreground'
}

export function PerformanceErrorRow(props: {
  item: PerformanceErrorItem
  locale: string
  trailing?: ReactNode
  detail?: boolean
}) {
  const { t } = useTranslation()
  const item = props.item
  const timestamp = item.created_at
    ? new Date(item.created_at * 1000).toLocaleString(props.locale)
    : '—'
  const user = item.username?.trim() || `#${item.user_id}`

  return (
    <div
      className={cn(
        'grid min-w-[91rem] grid-cols-[10rem_minmax(9rem,1fr)_minmax(9rem,1fr)_5rem_11rem_minmax(18rem,2fr)_minmax(11rem,1fr)_minmax(9rem,1fr)_5rem] gap-3 border-b px-5 py-3 text-xs last:border-b-0',
        props.detail && 'bg-muted/20'
      )}
    >
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
        className={cn(
          'text-muted-foreground min-w-0 font-mono',
          props.detail ? 'break-words whitespace-pre-wrap' : 'truncate'
        )}
        title={item.error_reason}
      >
        {displayValue(item.error_reason)}
      </span>
      <span
        className={cn(
          'text-muted-foreground min-w-0 font-mono',
          props.detail ? 'break-all' : 'truncate'
        )}
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
      <span className='flex items-start justify-end'>{props.trailing}</span>
    </div>
  )
}
