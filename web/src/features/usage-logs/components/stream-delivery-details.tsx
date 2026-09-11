import { useTranslation } from 'react-i18next'

import type { LogOtherData } from '../types'

type Delivery = NonNullable<LogOtherData['stream_status']>['delivery']

export function StreamDeliveryDetails(props: { delivery?: Delivery }) {
  const { t } = useTranslation()
  if (!props.delivery) return null
  const rows = [
    [t('Terminal Event'), props.delivery.terminal_event],
    [t('Response Status'), props.delivery.response_status],
    [t('Incomplete Reason'), props.delivery.incomplete_reason],
    [t('Terminal Write'), props.delivery.terminal_write],
    [t('Usage Source'), props.delivery.usage_source],
    [
      t('Terminal Received (Unix ms)'),
      props.delivery.terminal_received_at_unix_ms,
    ],
    [
      t('Terminal Flushed (Unix ms)'),
      props.delivery.terminal_flushed_at_unix_ms,
    ],
    [
      t('Client Cancellation (Unix ms)'),
      props.delivery.client_canceled_at_unix_ms,
    ],
  ].filter(([, value]) => value !== undefined && value !== '' && value !== 0)
  if (!rows.length) return null
  return (
    <div className='space-y-2'>
      <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-xs'>
        {rows.map(([label, value]) => (
          <div key={label} className='contents'>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='min-w-0 font-mono break-all'>{value}</dd>
          </div>
        ))}
      </dl>
      <p className='text-muted-foreground text-xs'>
        {t('A successful local write does not confirm client receipt.')}
      </p>
    </div>
  )
}
