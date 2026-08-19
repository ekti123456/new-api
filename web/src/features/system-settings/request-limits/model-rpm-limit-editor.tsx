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
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { safeJsonParseWithValidation } from '../utils/json-parser'
import { isObjectRecord } from '../utils/json-validators'

interface ModelRPMLimitEditorProps {
  value: string
  onChange: (value: string) => void
}

interface ModelRPMEntry {
  model: string
  rpm: number
}

export function ModelRPMLimitEditor(props: ModelRPMLimitEditorProps) {
  const { t } = useTranslation()
  const [model, setModel] = useState('')
  const [rpm, setRPM] = useState(1)
  const entries = useMemo(() => {
    const parsed = safeJsonParseWithValidation<Record<string, unknown>>(
      props.value,
      {
        fallback: {},
        validator: isObjectRecord,
        silent: true,
      }
    )
    return Object.entries(parsed)
      .filter((entry): entry is [string, number] => {
        return typeof entry[1] === 'number' && entry[1] > 0
      })
      .map(([entryModel, entryRPM]) => ({
        model: entryModel,
        rpm: entryRPM,
      }))
      .sort((a, b) => a.model.localeCompare(b.model))
  }, [props.value])

  const saveEntries = (nextEntries: ModelRPMEntry[]) => {
    const value = Object.fromEntries(
      nextEntries.map((entry) => [entry.model, entry.rpm])
    )
    props.onChange(JSON.stringify(value, null, 2))
  }

  const handleAdd = () => {
    const normalizedModel = model.trim()
    if (!normalizedModel || rpm < 1) return
    const nextEntries = entries.filter(
      (entry) => entry.model !== normalizedModel
    )
    nextEntries.push({ model: normalizedModel, rpm })
    saveEntries(nextEntries)
    setModel('')
    setRPM(1)
  }

  const handleEdit = (entry: ModelRPMEntry) => {
    setModel(entry.model)
    setRPM(entry.rpm)
  }

  const handleDelete = (entryModel: string) => {
    saveEntries(entries.filter((entry) => entry.model !== entryModel))
  }

  return (
    <div className='space-y-4'>
      <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_9rem_auto]'>
        <Input
          value={model}
          onChange={(event) => setModel(event.target.value)}
          placeholder={t('Model name, e.g. gpt-5.2')}
          aria-label={t('Model name')}
        />
        <Input
          type='number'
          min={1}
          max={2147483647}
          value={rpm}
          onChange={(event) => setRPM(Number.parseInt(event.target.value) || 1)}
          aria-label={t('Requests per minute')}
        />
        <Button type='button' onClick={handleAdd} disabled={!model.trim()}>
          <Plus className='mr-2 h-4 w-4' />
          {t('Add model')}
        </Button>
      </div>

      <StaticDataTable
        data={entries}
        getRowKey={(entry) => entry.model}
        emptyContent={t(
          'No model-specific RPM limits configured. Unlisted models are not limited by this rule.'
        )}
        columns={[
          {
            id: 'model',
            header: t('Model'),
            cellClassName: 'font-mono',
            cell: (entry) => entry.model,
          },
          {
            id: 'rpm',
            header: t('RPM limit'),
            className: 'text-right',
            cellClassName: 'text-right font-mono',
            cell: (entry) => entry.rpm.toLocaleString(),
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'text-right',
            cellClassName: 'text-right',
            cell: (entry) => (
              <StaticRowActions
                editLabel={t('Edit')}
                deleteLabel={t('Delete')}
                menuLabel={t('Open menu')}
                onEdit={() => handleEdit(entry)}
                onDelete={() => handleDelete(entry.model)}
              />
            ),
          },
        ]}
      />
    </div>
  )
}
