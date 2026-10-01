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
import { useEffect, useMemo, useRef, useState } from 'react'
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
  warnings: string[]
  names: string[]
  options: PricingOptions
}

type OfficialPriceSyncProps = {
  modelNames?: string[]
  onSaved?: (names: string[]) => void
  onSavingChange?: (saving: boolean) => void
}

export function OfficialPriceSync(props: OfficialPriceSyncProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [preview, setPreview] = useState<Preview | null>(null)
  const [choices, setChoices] = useState<Record<string, string>>({})
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(0)
  const [confirm, setConfirm] = useState(false)
  const [error, setError] = useState('')
  const autoLoaded = useRef(false)

  const load = useMutation({
    mutationFn: async () => {
      if (props.modelNames !== undefined) {
        const [catalog, options] = await Promise.all([
          getOfficialPrices(),
          getPricingOptions(),
        ])
        return { ...catalog, names: [...new Set(props.modelNames)], options }
      }
      const [catalog, models, options] = await Promise.all([
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
      return { ...catalog, names, options }
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
        Object.fromEntries(
          rows.map((row) => [
            row.name,
            props.modelNames !== undefined
              ? Boolean(row.candidate) &&
                !row.blocked &&
                !row.candidate?.manual_only
              : row.autoSelect,
          ])
        )
      )
      setPreview(data)
      setPage(0)
    },
    onError: (cause: Error) => setError(cause.message),
  })

  const { mutate: loadPrices } = load
  useEffect(() => {
    if (props.modelNames === undefined || autoLoaded.current) return
    autoLoaded.current = true
    loadPrices()
  }, [props.modelNames, loadPrices])

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
        label: `${price.provider} / ${price.model} · ${price.currency ?? 'USD'} · ${price.source ?? 'models.dev'}${price.manual_only ? ` (${t('Use the model editor')})` : ''}`,
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
      props.onSaved?.(selections.map((selection) => selection.name))
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

  const onSavingChange = props.onSavingChange
  useEffect(() => {
    onSavingChange?.(save.isPending)
  }, [onSavingChange, save.isPending])

  const costText = (cost: Partial<OfficialPrice['cost']>) =>
    [cost.input, cost.output, cost.cache_read, cost.cache_write]
      .map((value) => (value === undefined ? '—' : formatPricingNumber(value)))
      .join(' / ')

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Chinese models use original CNY prices from LLM Abacus, with DeepSeek time bands from models-cn. International models use models.dev USD prices. Numbers are imported without currency conversion.'
        )}{' '}
        <a
          href='https://www.llmabacus.com'
          target='_blank'
          rel='noreferrer'
          className='underline'
        >
          LLM Abacus
        </a>
        {' · '}
        <a
          href='https://null-object-0000.github.io/models-cn/'
          target='_blank'
          rel='noreferrer'
          className='underline'
        >
          models-cn
        </a>
        {' · '}
        <a
          href='https://models.dev/api.json'
          target='_blank'
          rel='noreferrer'
          className='underline'
        >
          models.dev
        </a>
      </p>
      {props.modelNames !== undefined && (
        <p className='text-sm'>
          {t(
            'Syncing {{count}} models selected in the pricing lists. Exact matches are selected; choose sources for unmatched models.',
            { count: props.modelNames.length }
          )}
        </p>
      )}
      {props.modelNames === undefined && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Ignore case after removing slash-separated channel prefixes. Existing prices and different names require manual selection. Context tiers generate billing expressions; fixed prices use the model editor.'
          )}
        </p>
      )}
      <div className='flex flex-wrap gap-2'>
        {(props.modelNames === undefined || error) && (
          <Button
            onClick={() => load.mutate()}
            disabled={load.isPending || save.isPending}
          >
            {load.isPending ? t('Loading...') : t('Fetch official prices')}
          </Button>
        )}
        {props.modelNames !== undefined && load.isPending && (
          <span role='status' className='text-muted-foreground text-sm'>
            {t('Loading...')}
          </span>
        )}
        <Button
          variant='secondary'
          disabled={!preview || selections.length === 0 || save.isPending}
          onClick={() => {
            if (props.modelNames !== undefined) save.mutate()
            else setConfirm(true)
          }}
        >
          {props.modelNames !== undefined
            ? t('Confirm sync ({{count}})', { count: selections.length })
            : t('Save selected prices ({{count}})', {
                count: selections.length,
              })}
        </Button>
      </div>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {t(error)} {t('Fetch prices again before saving.')}
        </p>
      )}
      {preview && (
        <>
          {preview.warnings.map((warning) => (
            <p key={warning} role='alert' className='text-destructive text-sm'>
              {t(warning)}
            </p>
          ))}
          {props.modelNames !== undefined && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Confirming sync replaces the selected prices or expressions. Unselected models are unchanged.'
              )}
            </p>
          )}
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
            {props.modelNames !== undefined
              ? t(
                  'Prices: input / output / cache read / cache write. Review the matches and save; manually chosen sources are selected automatically.'
                )
              : t(
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
                              [row.name]:
                                props.modelNames !== undefined &&
                                Boolean(pricesByID.get(value)) &&
                                !pricesByID.get(value)?.manual_only,
                            }))
                          }}
                        />
                        {choice?.source_url?.startsWith('https://') && (
                          <a
                            className='mt-1 block text-xs underline'
                            href={choice.source_url}
                            target='_blank'
                            rel='noreferrer'
                          >
                            {t('Official pricing reference')}
                          </a>
                        )}
                        {choice?.verified_at && (
                          <p className='text-muted-foreground text-xs'>
                            {t('Source verification date: {{date}}', {
                              date: choice.verified_at.slice(0, 10),
                            })}
                          </p>
                        )}
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
                        {choice
                          ? `${choice.currency ?? 'USD'} ${costText(choice.cost)}`
                          : '—'}
                        {choice?.expression?.includes('cn_off_peak()') && (
                          <p className='text-muted-foreground mt-1 max-w-64 text-xs whitespace-normal'>
                            {t(
                              'Automatic peak/off-peak pricing (Beijing time, including holidays). The prices above are off-peak prices.'
                            )}
                          </p>
                        )}
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
