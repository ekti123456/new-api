import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Controller, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import { saveExpansionPolicy } from './api'
import type { ExpansionPolicy } from './types'

const schema = z.object({
  enabled: z.boolean(),
  extra_limit: z.number().int().min(1).max(100),
  multiplier: z.number().gt(1).max(10),
  multiplier_step: z.number().min(0.000001).max(9),
  channels: z.string().refine((value) => {
    const ids = value.split(',').map((part) => Number(part.trim()))
    return (
      ids.length <= 16 &&
      ids.every((id) => Number.isInteger(id) && id > 0) &&
      new Set(ids).size === ids.length
    )
  }),
})

export function WindowPolicyEditor(props: {
  policy: ExpansionPolicy
}): React.JSX.Element {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      ...props.policy,
      multiplier_step: props.policy.multiplier_step ?? 0.1,
      channels: props.policy.channel_ids.join(','),
    },
  })
  const step = form.watch('multiplier_step')
  const maximum = form.watch('multiplier')
  const extraLimit = form.watch('extra_limit')
  const mutation = useMutation({
    mutationFn: saveExpansionPolicy,
    onSuccess: () =>
      client.invalidateQueries({ queryKey: ['personal-windows'] }),
  })
  return (
    <details className='bg-card rounded-xl border p-4 sm:p-5'>
      <summary className='cursor-pointer font-medium'>
        {t('Expansion policy')}
      </summary>
      <form
        className='mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4'
        onSubmit={form.handleSubmit((values) =>
          mutation.mutate({
            enabled: values.enabled,
            extra_limit: values.extra_limit,
            multiplier: values.multiplier,
            multiplier_step: values.multiplier_step,
            channel_ids: values.channels.split(',').map(Number),
          })
        )}
      >
        <div className='bg-muted/30 col-span-full flex items-center justify-between gap-4 rounded-lg border p-3'>
          <span className='text-sm font-medium'>{t('Enable expansion')}</span>
          <Controller
            name='enabled'
            control={form.control}
            render={({ field }) => (
              <Switch
                aria-label={t('Enable expansion')}
                checked={field.value}
                onCheckedChange={field.onChange}
                disabled={mutation.isPending}
              />
            )}
          />
        </div>
        <label className='space-y-2 text-sm'>
          {t('Additional windows')}
          <Input
            type='number'
            min={1}
            max={100}
            {...form.register('extra_limit', { valueAsNumber: true })}
          />
        </label>
        <label className='space-y-2 text-sm'>
          {t('Multiplier increase per extra window')}
          <Input
            type='number'
            min={0.000001}
            max={9}
            step={0.000001}
            {...form.register('multiplier_step', { valueAsNumber: true })}
          />
        </label>
        <label className='space-y-2 text-sm'>
          {t('Maximum expansion multiplier')}
          <Input
            type='number'
            min={1.000001}
            max={10}
            step={0.000001}
            {...form.register('multiplier', { valueAsNumber: true })}
          />
        </label>
        <label className='space-y-2 text-sm'>
          {t('Codex2API channel IDs')}
          <Input placeholder='22,23' {...form.register('channels')} />
        </label>
        <div className='text-muted-foreground col-span-full space-y-2 text-sm'>
          <p>
            {t(
              'Each new extra window adds one tier. Its price stays fixed; the extra window limit still applies.'
            )}
          </p>
          {Number.isFinite(step) &&
            step >= 0.000001 &&
            Number.isFinite(maximum) &&
            maximum > 1 &&
            Number.isInteger(extraLimit) &&
            extraLimit > 0 && (
              <ol
                aria-label={t('Expansion price examples')}
                className='flex flex-wrap gap-x-4 gap-y-1 font-mono tabular-nums'
              >
                {Array.from({ length: Math.min(extraLimit, 6) }, (_, index) => (
                  <li key={index}>
                    {index + 1} → ×
                    {Number(
                      Math.min(maximum, 1 + (index + 1) * step).toFixed(6)
                    )}
                  </li>
                ))}
              </ol>
            )}
        </div>
        {Object.keys(form.formState.errors).length > 0 && (
          <p role='alert' className='text-destructive col-span-full'>
            {t('Invalid expansion policy')}
          </p>
        )}
        {mutation.isError && (
          <p role='alert' className='text-destructive col-span-full'>
            {mutation.error.message}
          </p>
        )}
        <Button type='submit' disabled={mutation.isPending}>
          {t('Save changes')}
        </Button>
      </form>
    </details>
  )
}
