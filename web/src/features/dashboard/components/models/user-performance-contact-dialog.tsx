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
import { BellRing, Loader2, Mail } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import {
  sendUserPerformanceAlert,
  type UserPerformanceAlertChannel,
} from '../../api'
import type { UserPerformanceAnomalyItem } from '../../types'

type UserPerformanceContactDialogProps = {
  item: UserPerformanceAnomalyItem | null
  apiInfoSummary: string
  channel: UserPerformanceAlertChannel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

function formatTemplateValue(value: string | undefined) {
  return value?.trim() || '—'
}

export function UserPerformanceContactDialog({
  item,
  apiInfoSummary,
  channel,
  open,
  onOpenChange,
}: UserPerformanceContactDialogProps) {
  const { t } = useTranslation()
  const [title, setTitle] = useState('')
  const [content, setContent] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [sending, setSending] = useState(false)

  useEffect(() => {
    if (!open || !item || !channel) return
    const username = item.username || `#${item.user_id}`
    const ttftSummary =
      item.ttft_count > 0
        ? `${item.above_group_avg_percentage.toFixed(1)}% (n=${item.ttft_count})`
        : t('No valid samples')
    setTitle(t('Request performance anomaly notice'))
    setContent(
      t('User performance alert template', {
        username,
        userId: item.user_id,
        group: item.group,
        accessUrl: formatTemplateValue(item.access_url),
        apiInfoSummary: formatTemplateValue(apiInfoSummary),
        ip: formatTemplateValue(item.ip),
        requestCount: item.request_count,
        errorRate: item.error_rate.toFixed(1),
        ttftSummary,
      })
    )
    setConfirmOpen(false)
  }, [apiInfoSummary, channel, item, open, t])

  const deliveryLabel =
    channel === 'email' ? t('Email') : t('Personal notification')
  const username = item?.username || (item ? `#${item.user_id}` : '')
  const canContinue = title.trim() !== '' && content.trim() !== ''

  const handleRequestConfirm = () => {
    if (!canContinue) {
      toast.error(t('Subject and content are required'))
      return
    }
    setConfirmOpen(true)
  }

  const handleConfirmSend = async () => {
    if (!item || !channel || !canContinue) return
    setSending(true)
    try {
      const response = await sendUserPerformanceAlert({
        user_id: item.user_id,
        channel,
        title: title.trim(),
        content: content.trim(),
      })
      if (!response.success) {
        throw new Error(response.message || t('Failed to send message'))
      }
      toast.success(t('Message sent successfully'))
      setConfirmOpen(false)
      onOpenChange(false)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to send message')
      )
    } finally {
      setSending(false)
    }
  }

  return (
    <>
      <Dialog
        open={open && Boolean(item && channel)}
        onOpenChange={onOpenChange}
        title={deliveryLabel}
        description={t('Review and edit the prefilled message before sending.')}
        contentClassName='sm:max-w-3xl'
        contentHeight='min(62vh, 38rem)'
        footer={
          <>
            <Button variant='outline' onClick={() => onOpenChange(false)}>
              {t('Cancel')}
            </Button>
            <Button onClick={handleRequestConfirm} disabled={!canContinue}>
              {channel === 'email' ? (
                <Mail className='size-4' />
              ) : (
                <BellRing className='size-4' />
              )}
              {t('Continue')}
            </Button>
          </>
        }
      >
        <div className='space-y-5'>
          <div className='bg-muted/40 grid gap-3 rounded-lg border p-3 text-xs sm:grid-cols-2'>
            <div>
              <span className='text-muted-foreground block'>
                {t('Recipient')}
              </span>
              <span className='font-semibold'>
                {username} · #{item?.user_id}
              </span>
            </div>
            <div>
              <span className='text-muted-foreground block'>
                {t('Delivery method')}
              </span>
              <span className='font-semibold'>{deliveryLabel}</span>
              {channel === 'email' && item?.email ? (
                <span className='text-muted-foreground ms-1 font-mono'>
                  · {item.email}
                </span>
              ) : null}
            </div>
          </div>

          <div className='space-y-2'>
            <Label htmlFor='performance-alert-title'>{t('Subject')}</Label>
            <Input
              id='performance-alert-title'
              value={title}
              maxLength={200}
              onChange={(event) => setTitle(event.target.value)}
            />
          </div>

          <div className='space-y-2'>
            <Label htmlFor='performance-alert-content'>{t('Content')}</Label>
            <Textarea
              id='performance-alert-content'
              value={content}
              maxLength={20000}
              className='min-h-72 resize-y font-mono text-sm leading-6'
              onChange={(event) => setContent(event.target.value)}
            />
            <div className='text-muted-foreground text-right text-xs tabular-nums'>
              {content.length.toLocaleString()} / 20,000
            </div>
          </div>
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Confirm send')}
        desc={t('Send this {{method}} to {{username}}?', {
          method: deliveryLabel,
          username,
        })}
        confirmText={
          <span className='inline-flex items-center gap-2'>
            {sending ? <Loader2 className='size-4 animate-spin' /> : null}
            {t('Send')}
          </span>
        }
        isLoading={sending}
        handleConfirm={() => void handleConfirmSend()}
      />
    </>
  )
}
