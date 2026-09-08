import { Clock3, Layers, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { remainingWindowTime } from './remaining-window-time'
import type { WindowPool } from './types'

export function WindowPoolCard(props: {
  pool: WindowPool
  now: number
}): React.JSX.Element {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(50)
  const windows = (props.pool.status.windows || []).filter(
    (window) => Date.parse(window.expires_at) > props.now
  )
  const expanded = windows.filter((window) => window.expanded).length
  const ready =
    !props.pool.status.creation_available_at ||
    Date.parse(props.pool.status.creation_available_at) <= props.now
  let cooldown = t('Ready')
  if (props.pool.status.cooldown_unavailable) {
    cooldown = t('Evaluated on the next creation request')
  } else if (!ready) {
    cooldown = remainingWindowTime(
      props.pool.status.creation_available_at,
      props.now
    )
  }
  return (
    <section
      className='bg-card min-w-0 space-y-4 rounded-xl border p-4 shadow-sm sm:p-5'
      aria-label={props.pool.name}
    >
      <h3 className='text-sm font-semibold break-all'>{props.pool.name}</h3>
      {!props.pool.available ? (
        <p role='alert' className='text-destructive'>
          {t('Window service unavailable')} ·{' '}
          {t('Unavailable does not mean zero usage')}
        </p>
      ) : (
        <>
          <dl className='grid grid-cols-2 gap-3 sm:grid-cols-3'>
            <div
              aria-label={t('Standard windows')}
              className='bg-muted/40 space-y-2 rounded-lg p-3'
            >
              <dt className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                <Layers className='size-3.5' aria-hidden='true' />
                {t('Standard windows')}
              </dt>
              <dd className='text-xl font-semibold tabular-nums'>
                {props.pool.status.truncated && '≥'}
                {windows.length - expanded}
                <span className='text-muted-foreground text-sm font-normal'>
                  /{props.pool.status.limit || '—'}
                </span>
              </dd>
            </div>
            <div
              aria-label={t('Expanded windows')}
              className='bg-primary/5 space-y-2 rounded-lg p-3'
            >
              <dt className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                <Sparkles className='size-3.5' aria-hidden='true' />
                {t('Expanded windows')}
              </dt>
              <dd className='text-xl font-semibold tabular-nums'>
                {props.pool.status.truncated && '≥'}
                {expanded}
              </dd>
            </div>
            <div className='bg-muted/40 col-span-2 space-y-2 rounded-lg p-3 sm:col-span-1'>
              <dt className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                <Clock3 className='size-3.5' aria-hidden='true' />
                {t('Creation cooldown')}
              </dt>
              <dd className='text-sm font-medium tabular-nums'>{cooldown}</dd>
            </div>
          </dl>
          {windows.length === 0 && (
            <p className='text-muted-foreground rounded-lg border border-dashed px-4 py-8 text-center text-sm'>
              {t('No active windows')}
            </p>
          )}
          {props.pool.status.truncated && (
            <p role='status'>
              {t('Showing the latest 1000 windows')} ·{' '}
              {t('Window usage snapshot')}: {props.pool.status.used}
            </p>
          )}
          <div className='grid gap-3 lg:grid-cols-2'>
            {windows.slice(0, visible).map((window) => (
              <article
                key={window.id}
                aria-label={window.id}
                className={cn(
                  'min-w-0 space-y-3 rounded-lg border p-4 text-sm',
                  window.expanded && 'border-primary/25 bg-primary/5'
                )}
              >
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <code
                    className='text-muted-foreground min-w-0 truncate text-xs'
                    title={window.id}
                  >
                    {window.id.slice(0, 16)}
                  </code>
                  <Badge variant={window.expanded ? 'default' : 'secondary'}>
                    {window.expanded
                      ? t('Expanded windows')
                      : t('Standard windows')}{' '}
                    · ×{window.multiplier}
                  </Badge>
                </div>
                <div className='flex flex-wrap items-baseline justify-between gap-2'>
                  <span className='text-muted-foreground text-xs'>
                    {t('Remaining')}
                  </span>
                  <span className='font-mono text-lg font-semibold tabular-nums'>
                    {remainingWindowTime(window.expires_at, props.now)}
                  </span>
                </div>
                <dl className='space-y-2 border-t pt-3 text-xs'>
                  <div className='flex flex-wrap justify-between gap-x-3 gap-y-1'>
                    <dt className='text-muted-foreground'>{t('Created at')}</dt>
                    <dd>
                      <time dateTime={window.created_at}>
                        {new Date(window.created_at).toLocaleString()}
                      </time>
                    </dd>
                  </div>
                  <div className='flex flex-wrap justify-between gap-x-3 gap-y-1'>
                    <dt className='text-muted-foreground'>
                      {t('Recovery time')}
                    </dt>
                    <dd>
                      <time dateTime={window.expires_at}>
                        {new Date(window.expires_at).toLocaleString()}
                      </time>
                    </dd>
                  </div>
                </dl>
                {window.model && (
                  <p className='text-muted-foreground text-xs break-all'>
                    {window.model}
                  </p>
                )}
              </article>
            ))}
          </div>
          {visible < windows.length && (
            <Button
              variant='outline'
              onClick={() => setVisible((previous) => previous + 50)}
            >
              {t('Load more')}
            </Button>
          )}
        </>
      )}
    </section>
  )
}
