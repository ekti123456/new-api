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
import {
  Calculator,
  CalendarDays,
  Clock3,
  CreditCard,
  Link2,
  Percent,
  ReceiptText,
  Share2,
  ShieldCheck,
  TrendingUp,
  Users,
  WalletCards,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import type { ReferralSummary } from '@/features/wallet/types'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

interface ReferralOverviewProps {
  affiliateLink: string
  loading: boolean
  onTransfer: () => void
  summary: ReferralSummary | null
}

export function ReferralOverview(props: ReferralOverviewProps) {
  const { t } = useTranslation()
  const currentRate = (props.summary?.rate_bps ?? 0) / 100
  const maxRate = (props.summary?.max_rate_bps ?? 0) / 100
  const usersPerTier = Math.max(props.summary?.users_per_tier ?? 1, 1)
  const isMaximumRate = currentRate >= maxRate
  const tierProgress = isMaximumRate
    ? 100
    : (((props.summary?.qualified_count ?? 0) % usersPerTier) / usersPerTier) *
      100
  const pendingRewards =
    (props.summary?.frozen_quota ?? 0) + (props.summary?.available_quota ?? 0)

  const stats: Array<{
    icon: typeof TrendingUp
    label: string
    tone: IconBadgeTone
    value: string
  }> = [
    {
      icon: TrendingUp,
      label: t('Total commission'),
      tone: 'success',
      value: formatQuota(props.summary?.history_quota ?? 0),
    },
    {
      icon: Percent,
      label: t('Commission rate'),
      tone: 'info',
      value: `${currentRate.toFixed(2)}%`,
    },
    {
      icon: WalletCards,
      label: t("Members' total top-up"),
      tone: 'warning',
      value: formatQuota(props.summary?.referred_topup_quota ?? 0),
    },
    {
      icon: Users,
      label: t('Invited members'),
      tone: 'neutral',
      value: (props.summary?.qualified_count ?? 0).toLocaleString(),
    },
    {
      icon: ReceiptText,
      label: t('Commission count'),
      tone: 'chart-1',
      value: (props.summary?.commission_count ?? 0).toLocaleString(),
    },
  ]

  const rules: Array<{
    description: string
    icon: typeof Calculator
    title: string
    tone: IconBadgeTone
  }> = [
    {
      description: t(
        'After an invitation joins this program, commission is calculated from each subsequent successful online top-up.'
      ),
      icon: Calculator,
      title: t('Calculation basis'),
      tone: 'info',
    },
    {
      description: t('Commission becomes transferable after a 24-hour freeze.'),
      icon: Clock3,
      title: t('Freeze and transfer'),
      tone: 'warning',
    },
    {
      description: t(
        'Only successful online top-ups count. A member qualifies after cumulative top-ups reach {{quota}}; redemption codes, subscriptions, and administrator credits do not count.',
        {
          quota: formatQuota(props.summary?.qualified_topup_quota ?? 0),
        }
      ),
      icon: ShieldCheck,
      title: t('Eligible sources'),
      tone: 'success',
    },
    {
      description: t(
        'Traceable historical invitations are included. Their past successful online top-ups count toward qualification, but past commissions are not backfilled.'
      ),
      icon: CalendarDays,
      title: t('Counting scope'),
      tone: 'neutral',
    },
  ]

  return (
    <div className='space-y-3 sm:space-y-4'>
      <Card data-card-hover='false' className='gap-0 py-0'>
        <CardContent className='grid gap-4 p-4 lg:grid-cols-[minmax(260px,1fr)_minmax(420px,.9fr)] lg:items-center lg:p-5'>
          <div className='flex min-w-0 items-center gap-3'>
            <IconBadge tone='info' size='lg'>
              <Share2 />
            </IconBadge>
            <div className='min-w-0'>
              <div className='flex flex-wrap items-center gap-2'>
                <h3 className='text-base font-semibold'>
                  {t('Your referral link')}
                </h3>
                <StatusBadge
                  label={t('Active now')}
                  variant='success'
                  copyable={false}
                  className='border-success/30 bg-success/5 border px-2'
                />
              </div>
              <p className='text-muted-foreground mt-0.5 text-sm'>
                {t('Share this link to invite new users and earn rewards.')}
              </p>
            </div>
          </div>

          {props.loading ? (
            <Skeleton className='h-10 w-full rounded-lg' />
          ) : (
            <div className='flex min-w-0 items-center gap-2'>
              <div className='relative min-w-0 flex-1'>
                <Link2 className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
                <Input
                  value={props.affiliateLink}
                  readOnly
                  aria-label={t('Your referral link')}
                  className='bg-muted/20 h-10 pr-3 pl-9 font-mono text-xs sm:text-sm'
                />
              </div>
              <CopyButton
                value={props.affiliateLink}
                variant='outline'
                className='size-10'
                iconClassName='size-4'
                tooltip={t('Copy referral link')}
                aria-label={t('Copy referral link')}
              />
            </div>
          )}
        </CardContent>
      </Card>

      <div
        className='grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5'
        data-referral-stat-grid
      >
        {stats.map((stat, index) => (
          <Card
            key={stat.label}
            data-card-hover='false'
            className={cn(
              'min-h-[116px] gap-0 py-0',
              index === stats.length - 1 &&
                'max-md:col-span-2 md:max-xl:col-span-2'
            )}
          >
            <CardContent className='flex h-full flex-col justify-between p-4'>
              <IconBadge tone={stat.tone} size='md'>
                <stat.icon />
              </IconBadge>
              <div className='mt-4 min-w-0'>
                {props.loading ? (
                  <Skeleton className='h-7 w-24' />
                ) : (
                  <p className='truncate font-mono text-2xl font-bold tracking-tight tabular-nums'>
                    {stat.value}
                  </p>
                )}
                <p className='text-muted-foreground mt-1 truncate text-xs'>
                  {stat.label}
                </p>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card data-card-hover='false' className='gap-0 py-0'>
        <CardContent className='grid gap-4 p-4 sm:grid-cols-2 lg:grid-cols-[minmax(240px,1.2fr)_repeat(3,minmax(120px,.65fr))_auto] lg:items-center lg:p-5'>
          <div className='flex min-w-0 items-center gap-3 sm:col-span-2 lg:col-span-1'>
            <IconBadge tone='success' size='lg'>
              <CreditCard />
            </IconBadge>
            <div className='min-w-0'>
              <h3 className='font-semibold'>{t('Reward balance')}</h3>
              <p className='text-muted-foreground mt-0.5 text-xs'>
                {t(
                  'Frozen rewards become transferable after the freeze period ends.'
                )}
              </p>
            </div>
          </div>

          {[
            [t('Rewards to transfer'), pendingRewards, 'text-foreground'],
            [
              t('Frozen commission'),
              props.summary?.frozen_quota ?? 0,
              'text-foreground',
            ],
            [
              t('Transferable'),
              props.summary?.available_quota ?? 0,
              'text-success',
            ],
          ].map(([label, value, valueClass]) => (
            <div key={String(label)} className='min-w-0'>
              <p className='text-muted-foreground truncate text-xs'>{label}</p>
              {props.loading ? (
                <Skeleton className='mt-1.5 h-6 w-24' />
              ) : (
                <p
                  className={cn(
                    'mt-1 truncate font-mono text-xl font-bold tabular-nums',
                    valueClass
                  )}
                >
                  {formatQuota(Number(value))}
                </p>
              )}
            </div>
          ))}

          <Button
            onClick={props.onTransfer}
            disabled={(props.summary?.available_quota ?? 0) <= 0}
            className='w-full sm:col-span-2 lg:col-span-1 lg:w-auto'
          >
            {t('Transfer to Balance')}
          </Button>
        </CardContent>
      </Card>

      <Card data-card-hover='false' className='gap-0 py-0'>
        <CardContent className='grid gap-4 p-4 lg:grid-cols-[240px_minmax(0,1fr)] lg:p-5'>
          <div className='flex items-start gap-3 lg:pt-2'>
            <IconBadge tone='info' size='lg'>
              <Users />
            </IconBadge>
            <div>
              <h3 className='text-base font-semibold'>
                {t('Commission calculation rules')}
              </h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Understand how invitations qualify and rewards settle.')}
              </p>
            </div>
          </div>

          <div className='grid gap-2 sm:grid-cols-2'>
            {rules.map((rule) => (
              <div
                key={rule.title}
                className='bg-muted/15 flex min-w-0 items-start gap-2.5 rounded-lg border p-3'
              >
                <IconBadge tone={rule.tone} size='sm'>
                  <rule.icon />
                </IconBadge>
                <div className='min-w-0'>
                  <h4 className='text-sm font-semibold'>{rule.title}</h4>
                  <p className='text-muted-foreground mt-0.5 text-xs leading-relaxed'>
                    {rule.description}
                  </p>
                </div>
              </div>
            ))}

            <div className='bg-muted/15 space-y-2 rounded-lg border p-3 sm:col-span-2'>
              <div className='flex flex-wrap items-center justify-between gap-2 text-xs'>
                <span>
                  {t('Current commission rate')}: {currentRate.toFixed(2)}%
                </span>
                <span className='text-muted-foreground'>
                  {t('Maximum commission rate')}: {maxRate.toFixed(2)}%
                </span>
              </div>
              <Progress value={tierProgress} />
              <p className='text-muted-foreground text-xs'>
                {isMaximumRate
                  ? t('Maximum commission rate reached')
                  : t(
                      'Invite {{count}} more qualified members to increase the commission rate by 1%.',
                      {
                        count: props.summary?.next_tier_remaining ?? 0,
                      }
                    )}
              </p>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
