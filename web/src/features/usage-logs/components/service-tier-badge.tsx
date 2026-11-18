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

import { tryParseRequestRuleExpr } from '@/features/pricing/lib/billing-expr'
import { cn } from '@/lib/utils'

import type { LogOtherData } from '../types'

const labels: Record<string, string> = {
  fast: 'Fast',
  priority: 'Fast',
  ultrafast: 'Ultrafast',
  default: 'Standard',
  auto: 'Auto',
  flex: 'Flex',
  scale: 'Scale',
}

function recordedTier(other: LogOtherData | undefined, isAdmin: boolean) {
  const field = isAdmin
    ? other?.admin_info?.billing_request?.service_tier
    : undefined
  if (field) {
    if (
      field.state !== 'string' ||
      field.redacted ||
      !field.value ||
      !Object.hasOwn(labels, field.value)
    ) {
      return null
    }
    return { value: field.value, source: 'request' as const }
  }
  // Old logs may only have the billing decision. Do not infer a requested tier
  // from upstream defaults, the model name, or rules that did not match.
  const matched = new Set<string>()
  for (const match of other?.request_rule_matches ?? []) {
    if (!match.matched) continue
    const groups = tryParseRequestRuleExpr(`(${match.expression})`)
    if (groups?.length !== 1) continue
    for (const condition of groups[0].conditions) {
      if (
        condition.source === 'param' &&
        condition.path === 'service_tier' &&
        condition.mode === 'eq' &&
        Object.hasOwn(labels, condition.value)
      ) {
        matched.add(condition.value)
      }
    }
  }
  if (matched.size !== 1) return null
  return { value: [...matched][0], source: 'billing' as const }
}

export function ServiceTierBadge(props: {
  other?: LogOtherData
  isAdmin?: boolean
}) {
  const { t } = useTranslation()
  const tier = recordedTier(props.other, props.isAdmin === true)
  if (!tier) return null
  const title =
    tier.source === 'request'
      ? t('Request service tier: {{tier}}', { tier: tier.value })
      : t('Matched billing tier: {{tier}}', { tier: tier.value })
  return (
    <span
      title={title}
      className={cn(
        'w-fit rounded px-1.5 py-0.5 text-[10px] font-medium',
        tier.value === 'ultrafast' && 'bg-chart-4/10 text-chart-4',
        (tier.value === 'fast' || tier.value === 'priority') &&
          'bg-info/10 text-info',
        !['fast', 'priority', 'ultrafast'].includes(tier.value) &&
          'bg-muted text-muted-foreground'
      )}
    >
      {labels[tier.value]}
    </span>
  )
}
