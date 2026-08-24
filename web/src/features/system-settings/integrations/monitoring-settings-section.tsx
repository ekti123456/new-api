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
import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { MultiSelect } from '@/components/multi-select'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { getGroups } from '@/features/users/api'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const numericString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  return !Number.isNaN(Number(trimmed)) && Number(trimmed) >= 0
}, 'Enter a non-negative number or leave empty')

const monitoringSchema = z.object({
  QuotaRemindThreshold: numericString,
  perf_metrics_setting: z.object({
    enabled: z.boolean(),
    flush_interval: z.coerce.number().min(1),
    bucket_time: z.enum(['minute', '5min', 'hour']),
    retention_days: z.coerce.number().min(0),
    user_anomaly_monitored_groups: z.array(z.string()),
    user_anomaly_min_requests: z.coerce.number().int().min(1).max(100000),
    user_error_rate_threshold: z.coerce.number().gt(0).max(100),
    user_ttft_over_average_percent: z.coerce.number().min(0).max(1000),
  }),
})

type MonitoringFormInput = z.input<typeof monitoringSchema>
type MonitoringFormValues = z.output<typeof monitoringSchema>

type FlatMonitoringDefaults = {
  QuotaRemindThreshold: string
  'perf_metrics_setting.enabled': boolean
  'perf_metrics_setting.flush_interval': number
  'perf_metrics_setting.bucket_time': 'minute' | '5min' | 'hour'
  'perf_metrics_setting.retention_days': number
  'perf_metrics_setting.user_anomaly_monitored_groups': string[]
  'perf_metrics_setting.user_anomaly_min_requests': number
  'perf_metrics_setting.user_error_rate_threshold': number
  'perf_metrics_setting.user_ttft_over_average_percent': number
}

type MonitoringSettingsSectionProps = {
  defaultValues: FlatMonitoringDefaults
}

const buildFormDefaults = (
  defaults: MonitoringSettingsSectionProps['defaultValues']
): MonitoringFormInput => ({
  QuotaRemindThreshold: defaults.QuotaRemindThreshold ?? '',
  perf_metrics_setting: {
    enabled: defaults['perf_metrics_setting.enabled'],
    flush_interval: defaults['perf_metrics_setting.flush_interval'],
    bucket_time: defaults['perf_metrics_setting.bucket_time'],
    retention_days: defaults['perf_metrics_setting.retention_days'],
    user_anomaly_monitored_groups:
      defaults['perf_metrics_setting.user_anomaly_monitored_groups'],
    user_anomaly_min_requests:
      defaults['perf_metrics_setting.user_anomaly_min_requests'],
    user_error_rate_threshold:
      defaults['perf_metrics_setting.user_error_rate_threshold'],
    user_ttft_over_average_percent:
      defaults['perf_metrics_setting.user_ttft_over_average_percent'],
  },
})

const normalizeDefaults = (
  defaults: MonitoringSettingsSectionProps['defaultValues']
): FlatMonitoringDefaults => ({
  QuotaRemindThreshold: (defaults.QuotaRemindThreshold ?? '').trim(),
  'perf_metrics_setting.enabled': defaults['perf_metrics_setting.enabled'],
  'perf_metrics_setting.flush_interval':
    defaults['perf_metrics_setting.flush_interval'],
  'perf_metrics_setting.bucket_time':
    defaults['perf_metrics_setting.bucket_time'],
  'perf_metrics_setting.retention_days':
    defaults['perf_metrics_setting.retention_days'],
  'perf_metrics_setting.user_anomaly_monitored_groups': [
    ...defaults['perf_metrics_setting.user_anomaly_monitored_groups'],
  ].sort(),
  'perf_metrics_setting.user_anomaly_min_requests':
    defaults['perf_metrics_setting.user_anomaly_min_requests'],
  'perf_metrics_setting.user_error_rate_threshold':
    defaults['perf_metrics_setting.user_error_rate_threshold'],
  'perf_metrics_setting.user_ttft_over_average_percent':
    defaults['perf_metrics_setting.user_ttft_over_average_percent'],
})

const normalizeFormValues = (
  values: MonitoringFormValues
): FlatMonitoringDefaults => ({
  QuotaRemindThreshold: values.QuotaRemindThreshold.trim(),
  'perf_metrics_setting.enabled': values.perf_metrics_setting.enabled,
  'perf_metrics_setting.flush_interval':
    values.perf_metrics_setting.flush_interval,
  'perf_metrics_setting.bucket_time': values.perf_metrics_setting.bucket_time,
  'perf_metrics_setting.retention_days':
    values.perf_metrics_setting.retention_days,
  'perf_metrics_setting.user_anomaly_monitored_groups': [
    ...new Set(values.perf_metrics_setting.user_anomaly_monitored_groups),
  ].sort(),
  'perf_metrics_setting.user_anomaly_min_requests':
    values.perf_metrics_setting.user_anomaly_min_requests,
  'perf_metrics_setting.user_error_rate_threshold':
    values.perf_metrics_setting.user_error_rate_threshold,
  'perf_metrics_setting.user_ttft_over_average_percent':
    values.perf_metrics_setting.user_ttft_over_average_percent,
})

export function MonitoringSettingsSection({
  defaultValues,
}: MonitoringSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [availableGroups, setAvailableGroups] = useState<string[]>([])
  const baselineRef = useRef<FlatMonitoringDefaults>(
    normalizeDefaults(defaultValues)
  )
  const baselineSerializedRef = useRef<string>(
    JSON.stringify(normalizeDefaults(defaultValues))
  )

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const form = useForm<MonitoringFormInput, unknown, MonitoringFormValues>({
    resolver: zodResolver(monitoringSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  useEffect(() => {
    const normalized = normalizeDefaults(defaultValues)
    const serialized = JSON.stringify(normalized)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = normalized
    baselineSerializedRef.current = serialized
  }, [defaultValues])

  const perfMetricsEnabled = form.watch('perf_metrics_setting.enabled')

  useEffect(() => {
    let cancelled = false
    void getGroups()
      .then((response) => {
        if (!cancelled) setAvailableGroups(response.data ?? [])
      })
      .catch(() => {
        if (!cancelled) toast.error(t('Unable to load groups'))
      })
    return () => {
      cancelled = true
    }
  }, [t])

  const onSubmit = async (values: MonitoringFormValues) => {
    const normalized = normalizeFormValues(values)
    const updates = (
      Object.keys(normalized) as Array<keyof FlatMonitoringDefaults>
    ).filter(
      (key) =>
        JSON.stringify(normalized[key]) !==
        JSON.stringify(baselineRef.current[key])
    )

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of updates) {
      await updateOption.mutateAsync({
        key,
        value: Array.isArray(normalized[key])
          ? JSON.stringify(normalized[key])
          : normalized[key],
      })
    }

    baselineRef.current = normalized
    baselineSerializedRef.current = JSON.stringify(normalized)
  }

  return (
    <SettingsSection title={t('Monitoring & Alerts')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='QuotaRemindThreshold'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Quota reminder (tokens)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    value={field.value}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t('Send email alerts when a user falls below this quota')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div>
            <h4 className='font-medium'>{t('Model performance metrics')}</h4>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t(
                'Collect relay latency and success-rate metrics for the model square.'
              )}
            </p>
          </div>

          <div className='grid grid-cols-1 gap-4 md:grid-cols-4'>
            <FormField
              control={form.control}
              name='perf_metrics_setting.enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Enable model performance metrics')}
                    </FormLabel>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <FormField
              control={form.control}
              name='perf_metrics_setting.flush_interval'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Flush interval (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      {...safeNumberFieldProps(field)}
                      disabled={!perfMetricsEnabled}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='perf_metrics_setting.bucket_time'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Aggregation bucket')}</FormLabel>
                  <Select
                    items={[
                      { value: 'minute', label: t('1 minute') },
                      { value: '5min', label: t('5 minutes') },
                      { value: 'hour', label: t('1 hour') },
                    ]}
                    value={field.value}
                    onValueChange={field.onChange}
                    disabled={!perfMetricsEnabled}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='minute'>{t('1 minute')}</SelectItem>
                        <SelectItem value='5min'>{t('5 minutes')}</SelectItem>
                        <SelectItem value='hour'>{t('1 hour')}</SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='perf_metrics_setting.retention_days'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Retention days')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      {...safeNumberFieldProps(field)}
                      disabled={!perfMetricsEnabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('0 means data is kept permanently')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='space-y-4 rounded-lg border p-4'>
            <div>
              <h4 className='font-medium'>
                {t('User performance anomaly monitoring')}
              </h4>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Only selected groups are sampled. Samples are retained for two hours and are visible only to the root administrator.'
                )}
              </p>
            </div>
            <FormField
              control={form.control}
              name='perf_metrics_setting.user_anomaly_monitored_groups'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Monitored groups')}</FormLabel>
                  <FormControl>
                    <MultiSelect
                      options={availableGroups.map((group) => ({
                        value: group,
                        label: group,
                      }))}
                      selected={field.value}
                      onChange={field.onChange}
                      placeholder={t('Select groups to monitor')}
                      disabled={!perfMetricsEnabled}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Leave empty to disable user anomaly monitoring.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='grid gap-4 md:grid-cols-3'>
              <FormField
                control={form.control}
                name='perf_metrics_setting.user_anomaly_min_requests'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Minimum request samples')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={100000}
                        step={1}
                        {...safeNumberFieldProps(field)}
                        disabled={!perfMetricsEnabled}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'The same minimum protects both first-token and error-rate detection from one-off requests.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='perf_metrics_setting.user_ttft_over_average_percent'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('First-token excess over group average (%)')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        max={1000}
                        step={1}
                        {...safeNumberFieldProps(field)}
                        disabled={!perfMetricsEnabled}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'A request is counted as slow only after its first-token time exceeds the group average by this percentage. For example, 50% turns 1.6s into 2.4s.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='perf_metrics_setting.user_error_rate_threshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('User error-rate threshold (%)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0.01}
                        max={100}
                        step={0.1}
                        {...safeNumberFieldProps(field)}
                        disabled={!perfMetricsEnabled}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Users above this error rate are shown after reaching the minimum request count.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
