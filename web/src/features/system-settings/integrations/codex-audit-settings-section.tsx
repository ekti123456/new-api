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
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import { SettingsCard } from '../components/settings-card'
import {
  SettingsForm,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import {
  blankAuditConnection,
  getAuditSettings,
  getAuditStatus,
  saveAuditSettings,
  type AuditEditor,
} from './codex-audit-api'
import { AuditConnectionFields } from './codex-audit-connection'

const schema = z.object({
  revision: z.string(),
  source: z.string(),
  cpa_supported: z.boolean(),
  settings: z.object({
    enabled: z.boolean(),
    identity_forward_enabled: z.boolean(),
    audit_enabled: z.boolean(),
    strike_enabled: z.boolean(),
    account_ban_enabled: z.boolean(),
    ip_block_enabled: z.boolean(),
    ban_after: z.number().int().min(1).max(1000),
    window_seconds: z.number().int().min(60).max(31536000),
    cpa_instance_id: z.string().max(256),
  }),
  connections: z
    .array(
      z.object({
        id: z.string(),
        target: z
          .string()
          .url()
          .refine((url) => /^https?:\/\//.test(url)),
        platform_id: z.string().regex(/^[a-z0-9][a-z0-9_-]{0,31}$/),
        api_key: z.string(),
        secret: z.string(),
        secret_configured: z.boolean(),
        codex_key_fingerprint: z.string(),
        enabled: z.boolean(),
        profile: z.string().optional(),
        mode: z.string().optional(),
      })
    )
    .max(32),
})

export function CodexAuditSettingsSection() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['codex-audit-settings'],
    queryFn: getAuditSettings,
    retry: false,
  })
  if (query.isPending) return <p>{t('Loading settings...')}</p>
  if (!query.data || query.isError)
    return (
      <div role='alert'>
        <p>{t('Unable to load audit settings')}</p>
        <Button onClick={() => query.refetch()}>{t('Retry')}</Button>
      </div>
    )
  return <AuditEditorForm initial={query.data} />
}

function AuditEditorForm(props: { initial: AuditEditor }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [removeIndex, setRemoveIndex] = useState<number | null>(null)
  const form = useForm<AuditEditor>({
    resolver: zodResolver(schema),
    defaultValues: props.initial,
  })
  const fields = useFieldArray({
    control: form.control,
    name: 'connections',
    keyName: 'fieldId',
  })
  const settings = form.watch('settings')
  const revision = form.watch('revision')
  const status = useQuery({
    queryKey: ['codex-audit-status', revision],
    queryFn: getAuditStatus,
    refetchInterval: 30000,
    refetchIntervalInBackground: false,
    retry: false,
    enabled: props.initial.connections.length > 0,
  })
  const save = useMutation({
    mutationFn: saveAuditSettings,
    onSuccess: (data) => {
      form.reset(data)
      client.setQueryData(['codex-audit-settings'], data)
      client.invalidateQueries({ queryKey: ['codex-audit-status'] })
      toast.success(t('Audit settings saved and applied'))
    },
  })

  const switches = [
    ['enabled', 'Enable Codex2API audit connection'],
    ['identity_forward_enabled', 'Forward signed user identity'],
    ['audit_enabled', 'Record audit decisions'],
    ['strike_enabled', 'Accumulate violations'],
    ['account_ban_enabled', 'Allow automatic account restrictions'],
    ['ip_block_enabled', 'Allow automatic IP restrictions'],
  ] as const

  return (
    <SettingsSection title={t('Codex2API Audit')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Changes apply immediately after saving. Connections use the existing Codex2API audit secret.'
        )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          props.initial.source === 'database'
            ? 'Configuration source: saved settings'
            : 'Configuration source: environment variables (until first save)'
        )}
      </p>
      <Form {...form}>
        <SettingsForm
          onSubmit={form.handleSubmit((data) => save.mutate(data))}
          autoComplete='off'
        >
          <SettingsPageFormActions
            onSave={form.handleSubmit((data) => save.mutate(data))}
            isSaving={save.isPending}
            onReset={() => form.reset(props.initial)}
            isResetDisabled={save.isPending}
          />
          <SettingsCard title={t('Audit behavior')}>
            {switches.map(([name, label]) => (
              <SettingsSwitchField
                key={name}
                label={t(label)}
                checked={settings[name]}
                disabled={save.isPending}
                onCheckedChange={(value) => {
                  form.setValue(`settings.${name}`, value, {
                    shouldDirty: true,
                  })
                  if (name === 'strike_enabled' && !value) {
                    form.setValue('settings.account_ban_enabled', false, {
                      shouldDirty: true,
                    })
                    form.setValue('settings.ip_block_enabled', false, {
                      shouldDirty: true,
                    })
                  } else if (
                    (name === 'account_ban_enabled' ||
                      name === 'ip_block_enabled') &&
                    value
                  ) {
                    form.setValue('settings.strike_enabled', true, {
                      shouldDirty: true,
                    })
                  }
                }}
              />
            ))}
            <div className='mt-4 grid gap-4 lg:grid-cols-2'>
              {(['ban_after', 'window_seconds'] as const).map((name) => (
                <FormField
                  key={name}
                  control={form.control}
                  name={`settings.${name}`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t(
                          name === 'ban_after'
                            ? 'Violation threshold'
                            : 'Violation window (seconds)'
                        )}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          disabled={save.isPending}
                          onChange={(e) =>
                            field.onChange(e.target.valueAsNumber)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ))}
            </div>
          </SettingsCard>
          {props.initial.cpa_supported && (
            <FormField
              control={form.control}
              name='settings.cpa_instance_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('CPA instance identity')}</FormLabel>
                  <FormControl>
                    <Input {...field} disabled={save.isPending} />
                  </FormControl>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Keep this value stable and unique to this NewAPI deployment. Empty disables CPA identity forwarding.'
                    )}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <span className='text-sm'>
              {t(
                'Saved connections are checked every 30 seconds while this page is open.'
              )}
            </span>
            <Button
              type='button'
              variant='outline'
              onClick={() => status.refetch()}
              disabled={status.isFetching || save.isPending}
            >
              {t('Refresh status')}
            </Button>
          </div>
          {fields.fields.length === 0 && (
            <p>{t('No audit connections configured')}</p>
          )}
          {fields.fields.map((field, index) => (
            <AuditConnectionFields
              key={field.fieldId}
              form={form}
              index={index}
              status={
                status.data?.revision === revision
                  ? status.data.data[field.id]
                  : undefined
              }
              checking={status.isFetching}
              disabled={save.isPending}
              onRemove={() => setRemoveIndex(index)}
            />
          ))}
          <Button
            type='button'
            variant='outline'
            disabled={fields.fields.length >= 32 || save.isPending}
            onClick={() => fields.append({ ...blankAuditConnection })}
          >
            {t('Add connection')}
          </Button>
          <p className='text-muted-foreground text-sm'>
            {t(
              'The calling key is stored in channel management; only its fingerprint is saved here. Unsaved tests do not change routing.'
            )}
          </p>
          {save.isError && (
            <p role='alert' className='text-destructive'>
              {t(save.error.message)}
            </p>
          )}
          {status.isError && (
            <p role='alert' className='text-destructive'>
              {t('Unable to refresh connection status')}
            </p>
          )}
          <Button type='submit' disabled={save.isPending}>
            {t(save.isPending ? 'Saving...' : 'Save Changes')}
          </Button>
        </SettingsForm>
      </Form>
      <ConfirmDialog
        open={removeIndex !== null}
        onOpenChange={(open) => {
          if (!open) setRemoveIndex(null)
        }}
        title={t('Remove connection')}
        desc={t(
          'Removal takes effect after saving. The Codex2API binding itself is not deleted.'
        )}
        confirmText={t('Remove')}
        destructive
        handleConfirm={() => {
          if (removeIndex !== null) fields.remove(removeIndex)
          setRemoveIndex(null)
        }}
      />
    </SettingsSection>
  )
}
