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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Button } from '@/components/ui/button'
import { Form, FormField } from '@/components/ui/form'
import { formatNumber } from '@/lib/format'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import {
  blankRateRule,
  getIndependentRates,
  independentRateSchema,
  saveIndependentRates,
  type RateEditor,
  type RateRule,
} from './independent-rate-api'
import { IndependentRateOverrides } from './independent-rate-overrides'
import { IndependentRateRuleDialog } from './independent-rate-rule-dialog'
import { IndependentRateUsage } from './independent-rate-usage'

export function IndependentRateSection() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['independent-rpm-settings'],
    queryFn: getIndependentRates,
    refetchOnWindowFocus: false,
    retry: false,
  })
  if (query.isPending) return <p>{t('Loading settings...')}</p>
  if (query.isError || !query.data) {
    return (
      <div role='alert'>
        <p>{t('Unable to load independent rate limits')}</p>
        <Button onClick={() => query.refetch()}>{t('Retry')}</Button>
      </div>
    )
  }
  return <IndependentRateEditor initial={query.data} />
}

function IndependentRateEditor(props: { initial: RateEditor }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<RateEditor>({
    resolver: zodResolver(independentRateSchema),
    defaultValues: props.initial,
  })
  const rules = form.watch('settings.rules')
  const [editing, setEditing] = useState<RateRule | null>(null)
  const [overrides, setOverrides] = useState<RateRule | null>(null)
  const [removing, setRemoving] = useState<RateRule | null>(null)
  const [usageID, setUsageID] = useState('')
  const mutation = useMutation({
    mutationFn: saveIndependentRates,
    onSuccess: (saved) => {
      form.reset(saved)
      client.setQueryData(['independent-rpm-settings'], saved)
      void client.invalidateQueries({ queryKey: ['independent-rpm-usage'] })
      toast.success(t('Independent rate limits saved'))
    },
  })
  const changeRules = (next: RateRule[]) =>
    form.setValue('settings.rules', next, {
      shouldDirty: true,
      shouldValidate: true,
    })
  const moveRule = (index: number, offset: number) => {
    const next = [...rules]
    const target = index + offset
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    changeRules(next)
  }
  const savedRule = props.initial.settings.rules.find(
    (rule) => rule.id === usageID
  )
  const streamLabels = {
    non_stream: t('Non-streaming'),
    stream: t('Streaming'),
    any: t('Any'),
  }
  return (
    <SettingsSection title={t('Independent request rate limits')}>
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit((value) => mutation.mutate(value))}
          className='space-y-4'
        >
          <FormField
            control={form.control}
            name='settings.enabled'
            render={({ field }) => (
              <SettingsSwitchField
                label={t('Enable independent request limits')}
                description={t(
                  'Independent of the model rate limit switch. Matching requests use only this pool; other request types keep their existing limits.'
                )}
                checked={field.value}
                onCheckedChange={field.onChange}
              />
            )}
          />
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Each user has a separate rolling 60-second pool. Rules are evaluated from top to bottom.'
              )}
            </p>
            <div className='flex gap-2'>
              <Button
                type='button'
                variant='outline'
                onClick={() => setEditing(blankRateRule())}
                disabled={rules.length >= 64}
              >
                + {t('Add rule')}
              </Button>
              <Button
                type='submit'
                disabled={mutation.isPending || !form.formState.isDirty}
              >
                {t('Save independent limits')}
              </Button>
            </div>
          </div>
          <StaticDataTable
            data={rules}
            getRowKey={(rule) => rule.id}
            emptyContent={t(
              'No independent rules. Other traffic is unchanged.'
            )}
            columns={[
              {
                id: 'name',
                header: t('Rule'),
                cell: (rule) => (
                  <div>
                    <span className='font-medium'>{rule.name}</span>
                    <p className='text-muted-foreground text-xs'>
                      {rule.enabled ? t('Enabled') : t('Disabled')}
                    </p>
                  </div>
                ),
              },
              {
                id: 'match',
                header: t('Request matching'),
                cell: (rule) => (
                  <div className='space-y-1'>
                    <code>{rule.path}</code>
                    <p className='text-muted-foreground text-xs'>
                      {rule.ua_mode === 'any' ? t('Any UA') : rule.ua}
                    </p>
                    <p className='text-xs'>{streamLabels[rule.stream]}</p>
                  </div>
                ),
              },
              {
                id: 'limit',
                header: t('Default RPM per user'),
                cell: (rule) => formatNumber(rule.limit),
              },
              {
                id: 'actions',
                header: t('Actions'),
                cell: (rule, index) => (
                  <div className='flex flex-wrap gap-1'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => setEditing(rule)}
                    >
                      {t('Edit')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => setOverrides(rule)}
                    >
                      + {t('User overrides')} (
                      {formatNumber(rule.overrides?.length ?? 0)})
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      disabled={
                        !props.initial.settings.rules.some(
                          (saved) => saved.id === rule.id
                        )
                      }
                      onClick={() => setUsageID(rule.id)}
                    >
                      {t('Usage')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      aria-label={`${t('Move up')} ${rule.name}`}
                      disabled={index === 0}
                      onClick={() => moveRule(index, -1)}
                    >
                      ↑
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      aria-label={`${t('Move down')} ${rule.name}`}
                      disabled={index === rules.length - 1}
                      onClick={() => moveRule(index, 1)}
                    >
                      ↓
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => setRemoving(rule)}
                    >
                      {t('Delete')}
                    </Button>
                  </div>
                ),
              },
            ]}
          />
        </form>
      </Form>
      {savedRule && (
        <IndependentRateUsage
          key={savedRule.id}
          ruleID={savedRule.id}
          name={savedRule.name}
        />
      )}
      {editing && (
        <IndependentRateRuleDialog
          key={editing.id}
          rule={editing}
          onClose={() => setEditing(null)}
          onSave={(rule) => {
            changeRules(
              rules.some((entry) => entry.id === rule.id)
                ? rules.map((entry) => (entry.id === rule.id ? rule : entry))
                : [...rules, rule]
            )
            setEditing(null)
          }}
        />
      )}
      {overrides && (
        <IndependentRateOverrides
          key={overrides.id}
          rule={overrides}
          onClose={() => setOverrides(null)}
          onSave={(values) => {
            changeRules(
              rules.map((rule) =>
                rule.id === overrides.id ? { ...rule, overrides: values } : rule
              )
            )
            setOverrides(null)
          }}
        />
      )}
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null)
        }}
        title={t('Delete independent rule')}
        desc={removing?.name ?? ''}
        destructive
        handleConfirm={() => {
          changeRules(rules.filter((rule) => rule.id !== removing?.id))
          setRemoving(null)
        }}
      />
    </SettingsSection>
  )
}
