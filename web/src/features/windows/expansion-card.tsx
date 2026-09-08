import { Layers, ShieldCheck, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'

import type { PersonalWindowsData } from './types'

export function ExpansionCard(props: {
  data: PersonalWindowsData
  pending: boolean
  error?: string
  onChange: (enabled: boolean) => void
  onConfirmPrice: () => void
}): React.JSX.Element {
  const { t } = useTranslation()
  const needsConfirmation =
    props.data.enabled &&
    props.data.accepted_multiplier < props.data.policy.multiplier
  let status = props.data.enabled ? t('Expansion enabled') : t('Not enabled')
  if (!props.data.policy.enabled) {
    status = t('Expansion is currently unavailable')
  } else if (needsConfirmation) {
    status = t('Price confirmation required')
  }
  return (
    <section
      aria-label={t('Window expansion')}
      aria-busy={props.pending}
      className={cn(
        'bg-card overflow-hidden rounded-xl border shadow-sm transition-colors',
        props.data.enabled && 'border-primary/30'
      )}
    >
      <div className='flex items-start justify-between gap-4 p-5 sm:p-6'>
        <div className='flex min-w-0 items-start gap-3'>
          <div className='bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-xl'>
            <Sparkles className='size-5' aria-hidden='true' />
          </div>
          <div className='min-w-0 space-y-1.5'>
            <div className='flex flex-wrap items-center gap-2'>
              <h2 className='font-semibold'>{t('Window expansion')}</h2>
              <Badge
                variant={
                  props.data.enabled && props.data.policy.enabled
                    ? 'default'
                    : 'secondary'
                }
              >
                {status}
              </Badge>
            </div>
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {t('More room when your standard windows are full')}
            </p>
          </div>
        </div>
        <div className='flex shrink-0 flex-col items-end gap-2 pt-1'>
          <Switch
            aria-label={t('Enable expanded windows')}
            aria-describedby='window-expansion-rules'
            checked={props.data.enabled}
            disabled={
              props.pending ||
              (!props.data.policy.enabled && !props.data.enabled)
            }
            onCheckedChange={props.onChange}
          />
          {props.pending && (
            <span role='status' className='text-muted-foreground text-xs'>
              {t('Saving...')}
            </span>
          )}
        </div>
      </div>
      <dl className='mx-5 mb-5 grid grid-cols-2 divide-x rounded-lg border sm:mx-6'>
        <div className='min-w-0 space-y-1.5 p-4'>
          <dt className='text-muted-foreground flex items-center gap-1.5 text-xs'>
            <Layers className='size-3.5 shrink-0' aria-hidden='true' />
            {t('Additional windows')}
          </dt>
          <dd className='text-2xl font-semibold tracking-tight tabular-nums'>
            +{props.data.policy.extra_limit}
          </dd>
        </div>
        <div className='min-w-0 space-y-1.5 p-4'>
          <dt className='text-muted-foreground text-xs'>
            {t('Expansion multiplier')}
          </dt>
          <dd className='space-y-1.5'>
            <span className='text-2xl font-semibold tracking-tight tabular-nums'>
              ×{props.data.policy.multiplier}
            </span>
            <p className='text-muted-foreground text-xs'>
              {t('Only additional windows cost more')}
            </p>
          </dd>
        </div>
      </dl>
      <div
        id='window-expansion-rules'
        className='bg-muted/30 space-y-2 border-t px-5 py-4 text-xs leading-relaxed sm:px-6'
      >
        <p className='flex items-center gap-1.5 font-medium'>
          <ShieldCheck
            className='text-primary size-3.5 shrink-0'
            aria-hidden='true'
          />
          {t('Standard windows first, no surcharge for enabling')}
        </p>
        <p className='text-muted-foreground'>
          {t(
            'Window prices stay fixed until expiry, even after disabling. Creation cooldown and account restrictions still apply.'
          )}
        </p>
        {needsConfirmation && props.data.policy.enabled && (
          <Button
            variant='outline'
            size='sm'
            onClick={props.onConfirmPrice}
            disabled={props.pending}
          >
            {t('Confirm updated expansion price')}
          </Button>
        )}
        {props.error && (
          <p role='alert' className='text-destructive'>
            {props.error}
          </p>
        )}
      </div>
    </section>
  )
}
