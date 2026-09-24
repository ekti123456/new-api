import { useTranslation } from 'react-i18next'

import type { BillingTierFieldDiagnostic, LogOtherData } from '../types'

type Diagnostic = NonNullable<LogOtherData['admin_info']>['billing_request']

export function BillingRequestDetails(props: {
  diagnostic?: Diagnostic
  isAdmin: boolean
}) {
  const { t } = useTranslation()
  if (!props.isAdmin || !props.diagnostic) return null

  function fieldValue(field: BillingTierFieldDiagnostic): string {
    if (field.state === 'absent') return t('Field not provided')
    if (field.state === 'unavailable') {
      return t('Billing request body unavailable')
    }
    if (field.redacted) return t('Unrecognized tier value redacted')
    if (field.value !== undefined) return JSON.stringify(field.value)
    return field.state
  }

  const rows = [
    [t('Billing snapshot source'), props.diagnostic.source],
    [t('Billing body capture state'), props.diagnostic.body_state],
    ['Content-Type', props.diagnostic.content_type],
    ['service_tier', fieldValue(props.diagnostic.service_tier)],
  ]
  if (props.diagnostic.service_tier_camel_case) {
    rows.push([
      'serviceTier',
      fieldValue(props.diagnostic.service_tier_camel_case),
    ])
  }
  if (props.diagnostic.codex2api) {
    rows.push(
      [t('codex2api execution tier'), props.diagnostic.codex2api.service_tier],
      [
        t('codex2api local billing tier'),
        props.diagnostic.codex2api.local_billing_service_tier || '-',
      ]
    )
  }
  if (props.diagnostic.effective_service_tier) {
    rows.push([
      t('Effective billing service tier'),
      JSON.stringify(props.diagnostic.effective_service_tier),
    ])
  }
  if (props.diagnostic.priority_match_source) {
    const sources: Record<string, string> = {
      request: t('Original user request'),
      codex2api: t('codex2api response'),
      none: t('No Priority tier matched'),
    }
    rows.push([
      t('Priority tier source'),
      sources[props.diagnostic.priority_match_source] ??
        props.diagnostic.priority_match_source,
    ])
  }
  return (
    <section className='space-y-2' aria-label={t('Inbound billing parameters')}>
      <h4 className='text-sm font-medium'>{t('Inbound billing parameters')}</h4>
      <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-xs'>
        {rows.map(([label, value]) => (
          <div key={label} className='contents'>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='min-w-0 font-mono break-all'>{value}</dd>
          </div>
        ))}
      </dl>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Original request values are retained. Priority can also match a trusted codex2api response; the multiplier is applied once.'
        )}
      </p>
    </section>
  )
}
