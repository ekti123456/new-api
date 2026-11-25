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
import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import {
  SettingsForm,
  SettingsFormGrid,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const groups = [
  {
    title: 'Connection limits',
    hint: 'Limits apply per instance. Zero allows unlimited connections.',
    fields: [
      ['max_connections', 'Total client connections', 0, 1000000],
      ['max_user_connections', 'Client connections per user', 0, 1000000],
      ['max_client_handshakes', 'Concurrent client handshakes', 0, 1000000],
      ['max_handshakes', 'Concurrent upstream handshakes', 0, 1000000],
      [
        'max_channel_connections',
        'Upstream connections per channel',
        0,
        1000000,
      ],
    ],
  },
  {
    title: 'Timeouts (seconds)',
    hint: 'Zero disables client handshake, first-frame and between-turn timeouts. Stream and shutdown timeouts inherit the service settings.',
    fields: [
      ['client_handshake_seconds', 'Client upgrade response', 0, 86400],
      ['handshake_seconds', 'Upstream handshake', 1, 86400],
      ['first_frame_seconds', 'Waiting for the first request', 0, 604800],
      ['idle_seconds', 'Idle between turns', 0, 604800],
      ['stream_seconds', 'Waiting for the next upstream event', 0, 604800],
      ['write_seconds', 'Writing a message', 1, 86400],
      ['shutdown_seconds', 'Graceful shutdown wait', 0, 86400],
    ],
  },
  {
    title: 'Maximum message size (MiB)',
    hint: 'Zero inherits the configured request or stream size limit.',
    fields: [
      ['client_message_mib', 'Client message', 0, 2048],
      ['upstream_message_mib', 'Upstream message', 0, 2048],
    ],
  },
] as const

type SettingName = (typeof groups)[number]['fields'][number][0]
type Settings = Record<SettingName, number>

function readSettings(raw: string) {
  let stored: Record<string, unknown> = {}
  try {
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      stored = parsed as Record<string, unknown>
    }
  } catch {
    // Display the service defaults for legacy, empty option values.
  }
  const values = {} as Settings
  for (const group of groups) {
    for (const [key] of group.fields) {
      const value = stored[key]
      values[key] = typeof value === 'number' ? value : 0
    }
  }
  if (stored.handshake_seconds == null) values.handshake_seconds = 45
  if (stored.write_seconds == null) values.write_seconds = 30
  return { stored, values }
}

export function ResponsesWebSocketSection(props: { value: string }) {
  const { t } = useTranslation()
  const update = useUpdateOption()
  const options = useMemo(() => readSettings(props.value), [props.value])
  const form = useForm<Settings>({
    defaultValues: options.values,
    mode: 'onChange',
  })
  useEffect(() => {
    form.reset(options.values)
  }, [form, options])
  const save = form.handleSubmit(async (values) => {
    if (!form.formState.isDirty) return
    const result = await update.mutateAsync({
      key: 'ResponsesWebSocketSettings',
      value: JSON.stringify({ ...options.stored, ...values }),
    })
    if (result.success) form.reset(values)
  })
  return (
    <SettingsSection title={t('Responses WebSocket')}>
      <Form {...form}>
        <SettingsForm onSubmit={save}>
          <SettingsPageFormActions
            onSave={save}
            isSaving={update.isPending}
            isSaveDisabled={!form.formState.isDirty}
          />
          {groups.map((group) => (
            <fieldset key={group.title} className='min-w-0 space-y-3'>
              <legend className='font-medium'>{t(group.title)}</legend>
              <p className='text-muted-foreground text-xs'>{t(group.hint)}</p>
              <SettingsFormGrid>
                {group.fields.map(([name, label, min, max]) => (
                  <FormField
                    key={name}
                    control={form.control}
                    name={name}
                    rules={{
                      validate: (v) =>
                        (Number.isInteger(v) && v >= min && v <= max) ||
                        t('Enter an integer from {{min}} to {{max}}.', {
                          min,
                          max,
                        }),
                    }}
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t(label)}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            min={min}
                            max={max}
                            step={1}
                            value={
                              Number.isFinite(field.value) ? field.value : ''
                            }
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
              </SettingsFormGrid>
            </fieldset>
          ))}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
