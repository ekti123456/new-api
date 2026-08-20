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
import { RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { getChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { getUserAgentStats } from '@/features/dashboard/api'
import type { UserAgentStatItem } from '@/features/dashboard/types'

import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

type Props = {
  defaultValues: {
    enabled: boolean
    whitelist: string
    channelIds: string
  }
}

function parseStringList(value: string): string[] {
  try {
    const parsed: unknown = JSON.parse(value || '[]')
    return Array.isArray(parsed)
      ? parsed.filter((item): item is string => typeof item === 'string')
      : []
  } catch {
    return []
  }
}

function normalizeWhitelist(value: string): string[] {
  return [
    ...new Set(
      value
        .split('\n')
        .map((item) => item.trim())
        .filter(Boolean)
    ),
  ]
}

export function UserAgentRoutingSection(props: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [enabled, setEnabled] = useState(props.defaultValues.enabled)
  const [whitelist, setWhitelist] = useState(props.defaultValues.whitelist)
  const [channelIds, setChannelIds] = useState<number[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [search, setSearch] = useState('')
  const [uaStats, setUaStats] = useState<UserAgentStatItem[]>([])
  const [statsLoading, setStatsLoading] = useState(false)

  useEffect(() => {
    setEnabled(props.defaultValues.enabled)
    setWhitelist(parseStringList(props.defaultValues.whitelist).join('\n'))
    try {
      const parsed = JSON.parse(props.defaultValues.channelIds || '[]')
      setChannelIds(
        Array.isArray(parsed)
          ? parsed.filter((id): id is number => Number.isInteger(id) && id > 0)
          : []
      )
    } catch {
      setChannelIds([])
    }
  }, [props.defaultValues])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const pageSize = 100
        const first = await getChannels({ p: 1, page_size: pageSize })
        const all = [...(first.data?.items || [])]
        const pages = Math.ceil((first.data?.total || all.length) / pageSize)
        for (let page = 2; page <= pages; page += 1) {
          const next = await getChannels({ p: page, page_size: pageSize })
          all.push(...(next.data?.items || []))
        }
        if (!cancelled) setChannels(all)
      } catch {
        if (!cancelled) toast.error(t('Unable to load channels'))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [t])

  const save = async () => {
    if (enabled && channelIds.length === 0) {
      toast.error(t('Please select at least one routing channel'))
      return
    }
    try {
      await updateOption.mutateAsync({
        key: 'user_agent_routing_setting.user_agent_whitelist',
        value: JSON.stringify(normalizeWhitelist(whitelist)),
      })
      await updateOption.mutateAsync({
        key: 'user_agent_routing_setting.channel_ids',
        value: JSON.stringify(channelIds),
      })
      await updateOption.mutateAsync({
        key: 'user_agent_routing_setting.enabled',
        value: String(enabled),
      })
      toast.success(t('Saved successfully'))
    } catch {
      toast.error(t('Failed to save'))
    }
  }

  const loadUserAgentStats = async () => {
    setStatsLoading(true)
    try {
      const endTimestamp = Math.floor(Date.now() / 1000)
      const response = await getUserAgentStats({
        start_timestamp: endTimestamp - 30 * 24 * 60 * 60,
        end_timestamp: endTimestamp,
      })
      setUaStats(
        (response.data?.items || []).filter(
          (item) => !item.is_other && item.client_family !== 'Unknown'
        )
      )
    } catch {
      toast.error(t('Unable to load User-Agent statistics'))
    } finally {
      setStatsLoading(false)
    }
  }

  const setUserAgentSelected = (clientFamily: string, selected: boolean) => {
    const current = normalizeWhitelist(whitelist)
    const next = selected
      ? [...new Set([...current, clientFamily])]
      : current.filter(
          (item) => item.toLowerCase() !== clientFamily.toLowerCase()
        )
    setWhitelist(next.join('\n'))
  }

  const visibleChannels = channels.filter((channel) => {
    const query = search.trim().toLowerCase()
    return (
      !query ||
      channel.name.toLowerCase().includes(query) ||
      (channel.base_url || '').toLowerCase().includes(query) ||
      String(channel.id).includes(query)
    )
  })

  const whitelistItems = normalizeWhitelist(whitelist)

  return (
    <SettingsSection title={t('User-Agent Routing')}>
      <div className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Whitelisted User-Agents keep normal channel dispatch; all other traffic uses only the selected routing channels.'
          )}
        </p>
        <label className='flex items-start gap-3 rounded-md border p-3'>
          <Checkbox
            checked={enabled}
            onCheckedChange={(checked) => setEnabled(checked === true)}
          />
          <span>
            <span className='block text-sm font-medium'>
              {t('Enable User-Agent routing')}
            </span>
            <span className='text-muted-foreground text-xs'>
              {t(
                'For example, adding Claude means Claude requests use normal dispatch while unmatched clients enter the routing pool.'
              )}
            </span>
          </span>
        </label>
        <div className='grid gap-1.5'>
          <Label>{t('User-Agent whitelist')}</Label>
          <Textarea
            rows={4}
            value={whitelist}
            onChange={(event) => setWhitelist(event.target.value)}
            placeholder={'Claude\nCodex Desktop'}
          />
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <p className='text-muted-foreground text-xs'>
              {t(
                'One match per line. Matching is case-insensitive and uses contains matching.'
              )}
            </p>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={loadUserAgentStats}
              disabled={statsLoading}
            >
              <RefreshCw
                aria-hidden='true'
                className={statsLoading ? 'animate-spin' : ''}
              />
              {t('Load recent User-Agent statistics')}
            </Button>
          </div>
          {uaStats.length > 0 && (
            <div className='grid gap-1 rounded-md border p-2 sm:grid-cols-2'>
              {uaStats.map((item) => {
                const selected = whitelistItems.some(
                  (value) =>
                    value.toLowerCase() === item.client_family.toLowerCase()
                )
                return (
                  <label
                    key={item.client_family}
                    className='hover:bg-muted flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm'
                  >
                    <Checkbox
                      checked={selected}
                      onCheckedChange={(checked) =>
                        setUserAgentSelected(
                          item.client_family,
                          checked === true
                        )
                      }
                    />
                    <span className='min-w-0 flex-1 truncate'>
                      {item.client_family}
                    </span>
                    <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>
                      {item.percentage.toFixed(1)}%
                    </span>
                  </label>
                )
              })}
            </div>
          )}
        </div>
        <div className='grid gap-2'>
          <Label>{t('Routing channels')}</Label>
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={t('Search channels by name or URL')}
          />
          <div className='max-h-64 space-y-1 overflow-y-auto rounded-md border p-2'>
            {visibleChannels.map((channel) => (
              <label
                key={channel.id}
                className='hover:bg-muted flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm'
              >
                <Checkbox
                  checked={channelIds.includes(channel.id)}
                  onCheckedChange={(checked) =>
                    setChannelIds((current) =>
                      checked === true
                        ? [...new Set([...current, channel.id])]
                        : current.filter((id) => id !== channel.id)
                    )
                  }
                />
                <span className='min-w-0 flex-1 truncate'>{channel.name}</span>
                <span className='text-muted-foreground font-mono text-xs'>
                  #{channel.id}
                </span>
              </label>
            ))}
          </div>
        </div>
        <SettingsPageFormActions
          onSave={save}
          isSaving={updateOption.isPending}
        />
      </div>
    </SettingsSection>
  )
}
