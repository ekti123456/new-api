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
import { useTranslation } from 'react-i18next'

import type { LogOtherData } from '../types'

type RequestClassification = NonNullable<
  LogOtherData['admin_info']
>['request_classification']

export function RequestTypeBadge(props: {
  classification?: RequestClassification
}) {
  const { t } = useTranslation()
  if (!props.classification) {
    return (
      <span className='text-muted-foreground text-xs'>{t('Not recorded')}</span>
    )
  }

  const labels: Record<string, string> = {
    user: t('User request'),
    related_internal: t('Related background'),
    independent_internal: t('Independent background'),
    related_unclassified: t('Related unclassified'),
    compaction: t('Compaction'),
    unknown: t('Unknown'),
  }
  const label = labels[props.classification.type] ?? t('Unknown')
  const ingress = labels[props.classification.ingress_type] ?? t('Unknown')
  const source = [
    props.classification.thread_source,
    props.classification.subagent_kind,
  ]
    .filter(Boolean)
    .join(' / ')
  const showSource = source && source !== 'user'
  const changed =
    props.classification.ingress_type !== props.classification.type
  const title = [
    label,
    source,
    changed ? `${t('Ingress request type')}: ${ingress}` : '',
    props.classification.root_state,
  ]
    .filter(Boolean)
    .join('\n')

  return (
    <div className='flex max-w-52 flex-col items-start gap-0.5' title={title}>
      <span className='bg-muted rounded px-1.5 py-0.5 text-xs'>{label}</span>
      {showSource && (
        <span className='text-muted-foreground max-w-full truncate font-mono text-[10px]'>
          {source}
        </span>
      )}
      {changed && (
        <span className='text-muted-foreground text-[10px]'>
          {t('Ingress request type')}: {ingress}
        </span>
      )}
    </div>
  )
}
