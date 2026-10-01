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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import { SettingsCard } from '../components/settings-card'
import { SettingsSwitchField } from '../components/settings-form-layout'
import {
  testAuditConnection,
  type AuditEditor,
  type ProbeResult,
} from './codex-audit-api'

const states: Record<string, string> = {
  connected: 'Connection verified',
  disabled: 'Disabled',
  authentication_failed:
    'API key, audit secret or platform identity was rejected',
  endpoint_unavailable:
    'Verification endpoint unavailable; check the URL and Codex2API version',
  identity_mismatch: 'Platform identity or metadata does not match',
  rate_limited: 'Rate limited; check the calling key quota and limits',
  network_error: 'Cannot connect; check the URL, network and TLS certificate',
  invalid_response: 'Response verification failed',
  upgrade_required: 'Update Codex2API to verify the connection response',
  channel_key_missing:
    'Enter the calling key or configure a matching enabled channel',
  invalid_configuration: 'Invalid connection settings',
  upstream_error: 'Verification endpoint returned an error',
}

type Props = {
  form: UseFormReturn<AuditEditor>
  index: number
  status?: ProbeResult
  checking: boolean
  disabled: boolean
  onRemove: () => void
}

export function AuditConnectionFields(props: Props) {
  const { t } = useTranslation()
  const connection = props.form.watch(`connections.${props.index}`)
  const draft = JSON.stringify(connection)
  const [tested, setTested] = useState<{ draft: string; result: ProbeResult }>()
  const test = useMutation({
    mutationFn: testAuditConnection,
    onSuccess: (result, variables) =>
      setTested({ draft: JSON.stringify(variables), result }),
  })
  const dirty = props.form.formState.dirtyFields.connections?.[props.index]
  let result = props.status
  if (dirty) result = undefined
  if (tested?.draft === draft) result = tested.result
  const label = states[result?.state ?? ''] ?? 'Not checked'
  const canTest =
    connection.target.trim() !== '' && connection.platform_id.trim() !== ''

  return (
    <SettingsCard title={connection.platform_id || t('New connection')}>
      <div className='grid min-w-0 gap-4 lg:grid-cols-2'>
        {(['target', 'platform_id', 'api_key', 'secret'] as const).map(
          (name) => (
            <FormField
              key={name}
              control={props.form.control}
              name={`connections.${props.index}.${name}`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t(
                      {
                        target: 'Codex2API URL',
                        platform_id: 'Platform identity',
                        api_key: 'Calling API key',
                        secret: 'Audit secret',
                      }[name]
                    )}
                  </FormLabel>
                  <FormControl>
                    {name === 'api_key' || name === 'secret' ? (
                      <PasswordInput
                        {...field}
                        value={field.value ?? ''}
                        autoComplete='new-password'
                        disabled={props.disabled}
                        placeholder={
                          connection.id
                            ? t('Leave blank to keep the existing value')
                            : ''
                        }
                      />
                    ) : (
                      <Input
                        {...field}
                        type={name === 'target' ? 'url' : 'text'}
                        autoComplete='off'
                        disabled={props.disabled}
                        placeholder={
                          name === 'target'
                            ? 'https://codex.example.com'
                            : 'newapi-primary'
                        }
                      />
                    )}
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )
        )}
        {connection.codex_key_fingerprint && (
          <p className='text-muted-foreground text-xs break-all lg:col-span-2'>
            SHA-256: {connection.codex_key_fingerprint}
          </p>
        )}
        <SettingsSwitchField
          checked={connection.enabled}
          disabled={props.disabled}
          label={t('Enable connection')}
          onCheckedChange={(enabled) =>
            props.form.setValue(`connections.${props.index}.enabled`, enabled, {
              shouldDirty: true,
            })
          }
        />
        <div className='flex flex-wrap items-center gap-3 lg:col-span-2'>
          <Button
            type='button'
            variant='outline'
            disabled={!canTest || test.isPending || props.disabled}
            onClick={() => {
              setTested(undefined)
              test.mutate(connection)
            }}
          >
            {t(test.isPending ? 'Testing connection...' : 'Test connection')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            disabled={props.disabled}
            onClick={props.onRemove}
          >
            {t('Remove')}
          </Button>
          <span role='status' className='min-w-0 text-sm break-words'>
            {props.checking && !tested
              ? t('Checking saved connection...')
              : t(label)}
          </span>
        </div>
        {result?.checked_at && !result.checked_at.startsWith('0001') && (
          <p className='text-muted-foreground text-xs break-words lg:col-span-2'>
            {t('Last checked')}: {result.checked_at} · {result.latency_ms ?? 0}{' '}
            ms
            {result.http_status ? ` · HTTP ${result.http_status}` : ''}
          </p>
        )}
        {test.isError && (
          <p role='alert' className='text-destructive lg:col-span-2'>
            {t(test.error.message)}
          </p>
        )}
      </div>
    </SettingsCard>
  )
}
