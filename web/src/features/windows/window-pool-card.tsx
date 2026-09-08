import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

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
      className='min-w-0 space-y-3 rounded-xl border p-4'
      aria-label={props.pool.name}
    >
      <h2 className='font-semibold break-all'>{props.pool.name}</h2>
      {!props.pool.available ? (
        <p role='alert' className='text-destructive'>
          {t('Window service unavailable')} ·{' '}
          {t('Unavailable does not mean zero usage')}
        </p>
      ) : (
        <>
          <div className='flex flex-wrap gap-4 text-sm'>
            <span>
              {t('Standard windows')}: {props.pool.status.truncated && '≥'}
              {windows.length - expanded}/{props.pool.status.limit || '—'}
            </span>
            <span>
              {t('Expanded windows')}: {props.pool.status.truncated && '≥'}
              {expanded}
            </span>
            <span>
              {t('Creation cooldown')}: {cooldown}
            </span>
          </div>
          {windows.length === 0 && (
            <p className='text-muted-foreground'>{t('No active windows')}</p>
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
                className='bg-muted/40 min-w-0 space-y-2 rounded-lg p-3 text-sm'
              >
                <div className='flex flex-wrap justify-between gap-2'>
                  <code className='truncate' title={window.id}>
                    {window.id.slice(0, 16)}
                  </code>
                  <span>
                    {window.expanded
                      ? t('Expanded windows')
                      : t('Standard windows')}{' '}
                    · ×{window.multiplier}
                  </span>
                </div>
                <p>
                  {t('Created at')}:{' '}
                  {new Date(window.created_at).toLocaleString()}
                </p>
                <p>
                  {t('Recovery time')}:{' '}
                  {new Date(window.expires_at).toLocaleString()}
                </p>
                <p>
                  {t('Remaining')}:{' '}
                  <span className='tabular-nums'>
                    {remainingWindowTime(window.expires_at, props.now)}
                  </span>
                </p>
                {window.model && <p className='break-all'>{window.model}</p>}
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
