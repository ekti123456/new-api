import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { saveExpansionPolicy } from './api'
import type { ExpansionPolicy } from './types'

const schema = z.object({
  enabled: z.boolean(),
  extra_limit: z.number().int().min(1).max(100),
  multiplier: z.number().gt(1).max(10),
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
      channels: props.policy.channel_ids.join(','),
    },
  })
  const mutation = useMutation({
    mutationFn: saveExpansionPolicy,
    onSuccess: () =>
      client.invalidateQueries({ queryKey: ['personal-windows'] }),
  })
  return (
    <details className='rounded-xl border p-4'>
      <summary className='cursor-pointer font-medium'>
        {t('Expansion policy')}
      </summary>
      <form
        className='mt-4 grid gap-4 sm:grid-cols-3'
        onSubmit={form.handleSubmit((values) =>
          mutation.mutate({
            enabled: values.enabled,
            extra_limit: values.extra_limit,
            multiplier: values.multiplier,
            channel_ids: values.channels.split(',').map(Number),
          })
        )}
      >
        <label className='sm:col-span-3'>
          <input type='checkbox' {...form.register('enabled')} />{' '}
          {t('Enable expansion')}
        </label>
        <label>
          {t('Additional windows')}
          <Input
            type='number'
            min={1}
            max={100}
            {...form.register('extra_limit', { valueAsNumber: true })}
          />
        </label>
        <label>
          {t('Expansion multiplier')}
          <Input
            type='number'
            min={1.01}
            max={10}
            step={0.01}
            {...form.register('multiplier', { valueAsNumber: true })}
          />
        </label>
        <label>
          {t('Codex2API channel IDs')}
          <Input placeholder='22,23' {...form.register('channels')} />
        </label>
        {Object.keys(form.formState.errors).length > 0 && (
          <p role='alert' className='text-destructive sm:col-span-3'>
            {t('Invalid expansion policy')}
          </p>
        )}
        {mutation.isError && (
          <p role='alert' className='text-destructive sm:col-span-3'>
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
