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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { getResponsesWebSocketSnapshot } from '../api'

const gauges = [
  ['current', 'Client connections'],
  ['upstream_connections', 'Connected upstreams'],
  ['client_handshakes', 'Client handshakes'],
  ['upstream_handshakes', 'Upstream handshakes in progress'],
  ['waiting_first', 'Awaiting first request'],
  ['active', 'Processing a turn'],
  ['idle', 'Idle between turns'],
  ['closing', 'Closing'],
] as const
const totals = [
  ['peak', 'Peak client connections'],
  ['opened', 'Admitted connections'],
  ['released', 'Released connections'],
  ['dial_attempts', 'Upstream dial attempts'],
  ['dial_errors', 'Failed upstream handshakes'],
  ['dial_canceled', 'Canceled upstream handshakes'],
] as const
const reasons: Record<string, string> = {
  instance: 'Instance limit',
  user: 'User limit',
  client_handshakes: 'Client handshake limit',
  handshakes: 'Handshake limit',
  channel: 'Channel limit',
  shutdown: 'Shutdown',
  shutdown_timeout: 'Shutdown deadline',
  upgrade_failed: 'Client handshake failed',
  authorization: 'Authorization failed',
  client_closed: 'Client disconnected',
  upstream_closed: 'Upstream disconnected',
  first_frame_timeout: 'First request timed out',
  idle_timeout: 'Idle timeout',
  stream_timeout: 'Stream timeout',
  channel_revoked: 'Channel revoked',
  transport_error: 'Transport error',
}

export function ResponsesWebSocketPanel() {
  const { t, i18n } = useTranslation()
  const query = useQuery({
    queryKey: ['system-info', 'responses-websocket'],
    queryFn: getResponsesWebSocketSnapshot,
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
  })
  const data = query.data
  const counts = data?.connections
  const limits: Partial<Record<(typeof gauges)[number][0], number>> = {
    current: counts?.max_connections,
    client_handshakes: counts?.max_client_handshakes,
    upstream_handshakes: counts?.max_handshakes,
  }
  return (
    <section
      className='space-y-4 rounded-xl border p-4'
      aria-label={t('Responses WebSocket')}
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h2 className='font-semibold'>{t('Responses WebSocket')}</h2>
        <Button
          size='sm'
          variant='outline'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      {query.isError && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Could not refresh connection statistics.')}
        </p>
      )}
      {query.isPending && <p role='status'>{t('Loading...')}</p>}
      {data && counts && (
        <>
          <p className='text-muted-foreground text-xs'>
            {t('Instance {{node}} · since {{time}}', {
              node: data.node,
              time: new Date(data.started_at * 1000).toLocaleString(
                i18n.language
              ),
            })}
            {counts.draining && (
              <strong className='text-amber-600'>
                {' '}
                · {t('Draining connections')}
              </strong>
            )}
          </p>
          <dl className='grid grid-cols-2 gap-3 md:grid-cols-4'>
            {gauges.map(([key, label]) => (
              <div key={key}>
                <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
                <dd className='font-medium tabular-nums'>
                  {counts[key].toLocaleString(i18n.language)}
                  {!!limits[key] && (
                    <span className='text-muted-foreground'>
                      {' '}
                      / {limits[key]?.toLocaleString(i18n.language)}
                    </span>
                  )}
                </dd>
              </div>
            ))}
          </dl>
          <details>
            <summary className='cursor-pointer text-sm font-medium'>
              {t('Connection totals and release reasons')}
            </summary>
            <dl className='mt-3 grid grid-cols-2 gap-3 md:grid-cols-3'>
              {totals.map(([key, label]) => (
                <div key={key}>
                  <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
                  <dd className='tabular-nums'>
                    {counts[key].toLocaleString(i18n.language)}
                  </dd>
                </div>
              ))}
            </dl>
            {(
              [
                ['Admission rejections', counts.rejections],
                ['Release reasons', counts.release_reasons],
              ] as const
            ).map(([title, entries]) => (
              <div key={title} className='mt-3 text-sm'>
                <h3 className='font-medium'>{t(title)}</h3>
                {Object.keys(entries).length === 0 ? (
                  <p className='text-muted-foreground'>{t('None')}</p>
                ) : (
                  <dl className='flex flex-wrap gap-x-4 gap-y-1'>
                    {Object.entries(entries)
                      .sort(([a], [b]) => a.localeCompare(b))
                      .map(([key, value]) => (
                        <div className='flex gap-2' key={key}>
                          <dt>{t(reasons[key] ?? key)}</dt>
                          <dd className='tabular-nums'>
                            {value.toLocaleString(i18n.language)}
                          </dd>
                        </div>
                      ))}
                  </dl>
                )}
              </div>
            ))}
          </details>
        </>
      )}
    </section>
  )
}
