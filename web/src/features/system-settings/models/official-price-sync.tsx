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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Input } from '@/components/ui/input'
import { getEnabledModels } from '@/features/channels/api'

import {
  getOfficialPrices,
  getPricingOptions,
  saveOfficialPrices,
} from './official-price-api'
import {
  buildOfficialPriceRows,
  officialPriceID,
  parsePricingOptions,
  type OfficialPrice,
  type PricingOptions,
} from './official-price-matching'
import { formatPricingNumber } from './pricing-format'

type Preview = {
  prices: OfficialPrice[]
  names: string[]
  options: PricingOptions
}

export function OfficialPriceSync() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [preview, setPreview] = useState<Preview | null>(null)
  const [choices, setChoices] = useState<Record<string, string>>({})
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(0)
  const [confirm, setConfirm] = useState(false)
  const [error, setError] = useState('')

  const load = useMutation({
    mutationFn: async () => {
      const [prices, models, options] = await Promise.all([
        getOfficialPrices(),
        getEnabledModels(),
        getPricingOptions(),
      ])
      if (!models.success) {
        throw new Error(models.message || 'Failed to load enabled models')
      }
      const maps = parsePricingOptions(options)
      const names = [
        ...new Set([
          ...(models.data ?? []),
          ...Object.values(maps).flatMap(Object.keys),
        ]),
      ]
      return { prices, names, options }
    },
    onMutate: () => {
      setError('')
      setPreview(null)
      setSelected({})
      setChoices({})
    },
    onSuccess: (data) => {
      const rows = buildOfficialPriceRows(data.names, data.prices, data.options)
      setChoices(
        Object.fromEntries(
          rows.map((row) => [
            row.name,
            row.candidate ? officialPriceID(row.candidate) : '',
          ])
        )
      )
      setSelected(
        Object.fromEntries(rows.map((row) => [row.name, row.autoSelect]))
      )
      setPreview(data)
      setPage(0)
    },
    onError: (cause: Error) => setError(cause.message),
  })

  const rows = useMemo(
    () =>
      preview
        ? buildOfficialPriceRows(preview.names, preview.prices, preview.options)
        : [],
    [preview]
  )
  const pricesByID = useMemo(
    () =>
      new Map(preview?.prices.map((price) => [officialPriceID(price), price])),
    [preview]
  )
  const sourceOptions = useMemo(
    () =>
      preview?.prices.map((price) => ({
        value: officialPriceID(price),
        label: `${price.provider} / ${price.model}${price.manual_only ? ` (${t('Use the model editor')})` : ''}`,
      })) ?? [],
    [preview, t]
  )
  const selections = rows.flatMap((row) => {
    const price = pricesByID.get(choices[row.name])
    return selected[row.name] && !row.blocked && price && !price.manual_only
      ? [{ name: row.name, price }]
      : []
  })
  const filtered = rows.filter((row) =>
    row.name.toLowerCase().includes(search.trim().toLowerCase())
  )
  const visible = filtered.slice(page * 20, (page + 1) * 20)

  const save = useMutation({
    mutationFn: async () => {
      if (!preview || selections.length === 0) return
      await saveOfficialPrices(preview.options, selections)
    },
    onSuccess: () => {
      toast.success(t('Official prices saved'))
      setConfirm(false)
      setPreview(null)
      setSelected({})
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      queryClient.invalidateQueries({ queryKey: ['pricing'] })
    },
    onError: (cause: Error) => {
      setError(cause.message)
      setConfirm(false)
      // Refresh after failures so retries always show current persisted values.
      setPreview(null)
      setSelected({})
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
    },
  })

  const costText = (cost: Partial<OfficialPrice['cost']>) =>
    [cost.input, cost.output, cost.cache_read, cost.cache_write]
      .map((value) => (value === undefined ? '—' : formatPricingNumber(value)))
      .join(' / ')

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Use official provider entries from models.dev. Price numbers are copied as-is per 1M tokens, without currency conversion.'
        )}{' '}
        <a
          href='https://models.dev/api.json'
          target='_blank'
          rel='noreferrer'
          className='underline'
        >
          models.dev
        </a>
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Ignore case after removing slash-separated channel prefixes. Existing prices and different names require manual selection. Context tiers generate billing expressions; fixed prices use the model editor.'
        )}
      </p>
      <div className='flex flex-wrap gap-2'>
        <Button
          onClick={() => load.mutate()}
          disabled={load.isPending || save.isPending}
        >
          {load.isPending ? t('Loading...') : t('Fetch official prices')}
        </Button>
        <Button
          variant='secondary'
          disabled={!preview || selections.length === 0 || save.isPending}
          onClick={() => setConfirm(true)}
        >
          {t('Save selected prices ({{count}})', { count: selections.length })}
        </Button>
      </div>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {t(error)} {t('Fetch prices again before saving.')}
        </p>
      )}
      {preview && (
        <>
          <Input
            aria-label={t('Search local models')}
            placeholder={t('Search local models')}
            value={search}
            onChange={(event) => {
              setSearch(event.target.value)
              setPage(0)
            }}
          />
          <p className='text-muted-foreground text-sm'>
            {t(
              'Prices: input / output / cache read / cache write. A dash means not provided. Flat-price imports preserve existing optional prices; expression imports replace the full pricing rule. Select a row after choosing its source.'
            )}
          </p>
          <div className='max-h-[60vh] overflow-auto rounded-md border'>
            <table className='w-full min-w-[900px] text-left text-sm'>
              <thead className='bg-background sticky top-0 z-10'>
                <tr>
                  <th className='p-3'>{t('Select')}</th>
                  <th className='p-3'>{t('Local model')}</th>
                  <th className='p-3'>{t('Official source')}</th>
                  <th className='p-3'>{t('Current prices')}</th>
                  <th className='p-3'>{t('Source prices')}</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((row, index) => {
                  const choice = pricesByID.get(choices[row.name])
                  const inputID = `official-source-${page}-${index}`
                  let status = t('Unset price')
                  if (row.configured) {
                    status = t('Existing price: select to overwrite')
                  }
                  if (row.blocked) status = t('Use the model editor')
                  return (
                    <tr key={row.name} className='border-t'>
                      <td className='p-3'>
                        <input
                          type='checkbox'
                          aria-label={t('Sync {{model}}', { model: row.name })}
                          checked={Boolean(selected[row.name])}
                          disabled={
                            row.blocked ||
                            !choice ||
                            choice.manual_only ||
                            save.isPending
                          }
                          onChange={(event) =>
                            setSelected((previous) => ({
                              ...previous,
                              [row.name]: event.target.checked,
                            }))
                          }
                        />
                      </td>
                      <td className='max-w-72 p-3 break-words'>
                        {row.name}
                        <p className='text-muted-foreground text-xs'>
                          {status}
                        </p>
                      </td>
                      <td className='min-w-80 p-3'>
                        <label htmlFor={inputID} className='sr-only'>
                          {t('Official source for {{model}}', {
                            model: row.name,
                          })}
                        </label>
                        <ComboboxInput
                          id={inputID}
                          options={sourceOptions}
                          value={choices[row.name] ?? ''}
                          disabled={row.blocked || save.isPending}
                          placeholder={t('Search official model or provider')}
                          onValueChange={(value) => {
                            setChoices((previous) => ({
                              ...previous,
                              [row.name]: value,
                            }))
                            setSelected((previous) => ({
                              ...previous,
                              [row.name]: false,
                            }))
                          }}
                        />
                        {!choice && (
                          <p className='text-muted-foreground mt-1 text-xs'>
                            {t(
                              'No unique official match. Choose a source manually.'
                            )}
                          </p>
                        )}
                        {choice?.manual_only && (
                          <p className='text-muted-foreground mt-1 text-xs'>
                            {t(
                              'This source includes unsupported special prices. Use the model editor; the base price alone cannot be synced.'
                            )}
                          </p>
                        )}
                      </td>
                      <td className='p-3 whitespace-nowrap'>
                        {row.currentExpression ? (
                          <details>
                            <summary>{t('Current expression')}</summary>
                            <pre className='max-w-96 text-xs break-words whitespace-pre-wrap'>
                              {row.currentExpression}
                            </pre>
                          </details>
                        ) : (
                          costText(row.current)
                        )}
                      </td>
                      <td className='p-3 whitespace-nowrap'>
                        {choice ? costText(choice.cost) : '—'}
                        {choice?.expression && (
                          <details className='mt-1'>
                            <summary>
                              {t('Generated tiered expression')}
                            </summary>
                            <pre className='max-w-96 text-xs break-words whitespace-pre-wrap'>
                              {choice.expression}
                            </pre>
                          </details>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
            {visible.length === 0 && (
              <p className='p-4 text-sm'>{t('No local models found')}</p>
            )}
          </div>
          <div className='flex items-center gap-3'>
            <Button
              variant='outline'
              disabled={page === 0}
              onClick={() => setPage((value) => value - 1)}
            >
              {t('Previous')}
            </Button>
            <span className='text-sm'>
              {page + 1} / {Math.max(1, Math.ceil(filtered.length / 20))}
            </span>
            <Button
              variant='outline'
              disabled={(page + 1) * 20 >= filtered.length}
              onClick={() => setPage((value) => value + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </>
      )}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t('Save selected official prices?')}
        desc={t(
          'Save prices for {{count}} selected models using the displayed numbers. Selected existing prices or expressions will be replaced. No currency conversion is applied.',
          { count: selections.length }
        )}
        confirmText={t('Save')}
        isLoading={save.isPending}
        handleConfirm={() => save.mutate()}
      />
    </div>
  )
}
