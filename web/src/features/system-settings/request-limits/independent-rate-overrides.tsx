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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { searchUsers } from '@/features/users/api'
import { useDebounce } from '@/hooks/use-debounce'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  overrideSchema,
  type RateRule,
  type UserOverride,
} from './independent-rate-api'

const schema = z.object({ overrides: z.array(overrideSchema).max(2000) })
export function IndependentRateOverrides(props: {
  rule: RateRule
  onClose: () => void
  onSave: (overrides: UserOverride[]) => void
}) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const keyword = useDebounce(search, 300)
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: { overrides: props.rule.overrides },
  })
  const fields = useFieldArray({ control: form.control, name: 'overrides' })
  const users = useQuery({
    queryKey: ['independent-rpm-users', keyword],
    queryFn: async () =>
      requireServerSuccess(await searchUsers({ keyword, page_size: 10 })).data,
    enabled: keyword.trim().length > 0,
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('User RPM overrides')}
      description={props.rule.name}
      contentClassName='sm:max-w-[720px]'
      footer={
        <>
          <Button variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button type='submit' form='independent-overrides-form'>
            {t('Apply')}
          </Button>
        </>
      }
    >
      <p className='text-muted-foreground mb-3 text-sm'>
        {t(
          'Users without an override use the default RPM. Removing an override restores that default.'
        )}
      </p>
      <Input
        aria-label={t('Search users by ID or name')}
        placeholder={t('Search users by ID or name')}
        value={search}
        onChange={(event) => setSearch(event.target.value)}
      />
      {keyword.trim() && (
        <StaticDataTable
          data={users.data?.items ?? []}
          getRowKey={(row) => row.id}
          emptyContent={
            users.isFetching ? t('Loading...') : t('No users found')
          }
          columns={[
            {
              id: 'user',
              header: t('User'),
              cell: (row) => `${row.username} (#${row.id})`,
            },
            {
              id: 'add',
              header: t('Actions'),
              cell: (row) => (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={fields.fields.some(
                    (field) => field.user_id === row.id
                  )}
                  onClick={() =>
                    fields.append({ user_id: row.id, limit: props.rule.limit })
                  }
                >
                  + {t('Add override')}
                </Button>
              ),
            },
          ]}
        />
      )}
      <Form {...form}>
        <form
          id='independent-overrides-form'
          onSubmit={form.handleSubmit((value) => props.onSave(value.overrides))}
          className='mt-4'
        >
          <StaticDataTable
            data={fields.fields}
            getRowKey={(row) => row.id}
            emptyContent={t('No user overrides')}
            columns={[
              {
                id: 'user',
                header: t('User ID'),
                cell: (row) => formatNumber(row.user_id),
              },
              {
                id: 'limit',
                header: 'RPM',
                cell: (row, index) => (
                  <FormField
                    control={form.control}
                    name={`overrides.${index}.limit`}
                    render={({ field }) => (
                      <FormItem>
                        <FormControl>
                          <Input
                            aria-label={`${t('User ID')} ${row.user_id} RPM`}
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
                ),
              },
              {
                id: 'remove',
                header: t('Actions'),
                cell: (_, index) => (
                  <Button
                    type='button'
                    variant='ghost'
                    onClick={() => fields.remove(index)}
                  >
                    {t('Remove')}
                  </Button>
                ),
              },
            ]}
          />
        </form>
      </Form>
    </Dialog>
  )
}
