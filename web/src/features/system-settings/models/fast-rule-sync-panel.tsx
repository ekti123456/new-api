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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'

import { buildFastRuleSync, type FastRuleStatus } from './fast-rule-sync'
import { getPricingOptions, saveSelectedPricing } from './official-price-api'

export function FastRuleSyncPanel(props: {
  modelNames: string[]
  onSaved: (names: string[]) => void
  onSavingChange: (saving: boolean) => void
}) {
  const { t } = useTranslation()
  const statusLabels: Record<FastRuleStatus, string> = {
    ready: t('Ready to sync'),
    unchanged: t('Already synced'),
    unsupported: t('Skipped: not an OpenAI text model'),
    fixed: t('Skipped: fixed pricing'),
    unpriced: t('Set input and output prices first'),
    manual: t('Edit complex request rules in the model editor'),
  }
  const client = useQueryClient()
  const preview = useQuery({
    queryKey: ['fast-rule-preview', props.modelNames],
    queryFn: getPricingOptions,
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  const plan = preview.data
    ? buildFastRuleSync(preview.data, props.modelNames)
    : null
  const names =
    plan?.rows.filter((row) => row.status === 'ready').map((row) => row.name) ??
    []
  const save = useMutation({
    mutationFn: async () => {
      if (!preview.data || names.length === 0) return
      await saveSelectedPricing(
        preview.data,
        names,
        (current) => buildFastRuleSync(current, names).options
      )
      return names
    },
    onSuccess: (saved) => {
      if (!saved) return
      toast.success(t('Fast rules synced'))
      client.invalidateQueries({ queryKey: ['system-options'] })
      client.invalidateQueries({ queryKey: ['pricing'] })
      props.onSaved(saved)
    },
  })
  const onSavingChange = props.onSavingChange
  useEffect(
    () => onSavingChange(save.isPending),
    [save.isPending, onSavingChange]
  )
  const error = save.error ?? preview.error

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        Fast / Priority ×2 · Ultrafast ×6
      </p>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {t(error.message)}
        </p>
      )}
      {preview.isFetching && <p role='status'>{t('Loading...')}</p>}
      {!preview.isFetching && plan && (
        <div className='max-h-[50dvh] overflow-auto rounded-md border'>
          <table className='w-full text-sm'>
            <thead>
              <tr>
                <th className='p-2 text-left'>{t('Model')}</th>
                <th className='p-2 text-left'>{t('Status')}</th>
              </tr>
            </thead>
            <tbody>
              {plan.rows.map((row) => (
                <tr key={row.name} className='border-t'>
                  <td className='p-2 break-all'>{row.name}</td>
                  <td className='p-2'>{statusLabels[row.status]}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className='flex justify-end gap-2'>
        <Button
          variant='outline'
          disabled={preview.isFetching || save.isPending}
          onClick={() => {
            save.reset()
            void preview.refetch()
          }}
        >
          {t('Refresh')}
        </Button>
        <Button
          disabled={
            preview.isFetching ||
            preview.isError ||
            save.isPending ||
            save.isError ||
            names.length === 0
          }
          onClick={() => save.mutate()}
        >
          {t('Sync Fast rules ({{count}})', { count: names.length })}
        </Button>
      </div>
    </div>
  )
}
