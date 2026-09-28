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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  SettingsFormGrid,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { rateRuleSchema, type RateRule } from './independent-rate-api'

export function IndependentRateRuleDialog(props: {
  rule: RateRule
  onClose: () => void
  onSave: (rule: RateRule) => void
}) {
  const { t } = useTranslation()
  const form = useForm<RateRule>({
    resolver: zodResolver(rateRuleSchema),
    defaultValues: props.rule,
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Independent rate rule')}
      description={t(
        'All conditions must match the original request. The first matching rule wins.'
      )}
      contentClassName='sm:max-w-[680px]'
      contentHeight='auto'
      footer={
        <>
          <Button variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button type='submit' form='independent-rule-form'>
            {t('Apply')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='independent-rule-form'
          onSubmit={form.handleSubmit(props.onSave)}
        >
          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Rule name')}</FormLabel>
                  <FormControl>
                    <Input {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='path'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Exact request path')}</FormLabel>
                  <FormControl>
                    <Input {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='ua_mode'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('UA matching')}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      <NativeSelectOption value='any'>
                        {t('Any')}
                      </NativeSelectOption>
                      <NativeSelectOption value='exact'>
                        {t('Exact match')}
                      </NativeSelectOption>
                      <NativeSelectOption value='contains'>
                        {t('Contains')}
                      </NativeSelectOption>
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='ua'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('User-Agent')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      disabled={form.watch('ua_mode') === 'any'}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='stream'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Stream mode')}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
                      <NativeSelectOption value='non_stream'>
                        {t('Non-streaming')}
                      </NativeSelectOption>
                      <NativeSelectOption value='stream'>
                        {t('Streaming')}
                      </NativeSelectOption>
                      <NativeSelectOption value='any'>
                        {t('Any')}
                      </NativeSelectOption>
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='limit'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Default RPM per user')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={100000}
                      {...field}
                      onChange={(event) =>
                        field.onChange(event.target.valueAsNumber)
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchField
                  label={t('Enable this rule')}
                  checked={field.value}
                  onCheckedChange={field.onChange}
                />
              )}
            />
          </SettingsFormGrid>
        </form>
      </Form>
    </Dialog>
  )
}
