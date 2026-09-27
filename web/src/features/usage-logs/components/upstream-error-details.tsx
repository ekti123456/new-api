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
import { useTranslation } from 'react-i18next'

import type { LogOtherData } from '../types'

interface UpstreamErrorDetailsProps {
  diagnostic?: NonNullable<LogOtherData['admin_info']>['upstream_error']
  isAdmin: boolean
  compact?: boolean
}

export function UpstreamErrorDetails(props: UpstreamErrorDetailsProps) {
  const { t } = useTranslation()
  if (!props.isAdmin || !props.diagnostic?.message) return null
  const diagnostic = props.diagnostic
  if (props.compact) {
    return (
      <span className='truncate text-red-600 group-hover:underline dark:text-red-400'>
        {diagnostic.message}
      </span>
    )
  }
  const fields = [
    [t('Error code'), diagnostic.code],
    [t('Error type'), diagnostic.type],
    [t('Error source'), diagnostic.source],
    [t('Failure stage'), diagnostic.stage],
    [t('Transport'), diagnostic.transport],
    [
      t('Observed upstream HTTP status'),
      diagnostic.http_status || t('Not observed'),
    ],
    [t('Handshake HTTP status'), diagnostic.handshake_status],
    [t('Upstream Request ID'), diagnostic.upstream_request_id],
    [t('Gateway Request ID'), diagnostic.gateway_request_id],
  ]
  return (
    <section
      aria-label={t('Upstream error details (redacted)')}
      className='min-w-0 space-y-3 rounded-lg border border-red-500/20 p-3'
    >
      <h3 className='text-sm font-medium'>
        {t('Upstream error details (redacted)')}
      </h3>
      <p className='text-sm break-all whitespace-pre-wrap text-red-600 dark:text-red-400'>
        {diagnostic.message}
      </p>
      <dl className='grid min-w-0 grid-cols-1 gap-x-3 gap-y-1 text-xs sm:grid-cols-[auto_minmax(0,1fr)]'>
        {fields
          .filter(([, value]) => value)
          .map(([label, value]) => (
            <div key={label} className='contents'>
              <dt className='text-muted-foreground'>{label}</dt>
              <dd className='min-w-0 font-mono break-all'>{value}</dd>
            </div>
          ))}
      </dl>
    </section>
  )
}
