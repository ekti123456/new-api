import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { formatSessionWindowCountdown } from '@/lib/format'

interface SessionWindowValueProps {
  used: number
  limit: number
  nextRecoveryAt?: number
}

export function SessionWindowValue(props: SessionWindowValueProps) {
  const { t, i18n } = useTranslation()
  const [nowMs, setNowMs] = useState(Date.now)
  const recoveryMs = (props.nextRecoveryAt ?? 0) * 1000
  const hasRecovery =
    Number.isFinite(recoveryMs) && recoveryMs > 0 && recoveryMs <= 8.64e15

  useEffect(() => {
    setNowMs(Date.now())
    if (!hasRecovery || props.used <= 0) return
    const timer = setInterval(() => setNowMs(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [hasRecovery, props.used, recoveryMs])

  let recoveryLabel = t('Release time unavailable')
  let recoveryTitle: string | undefined
  if (hasRecovery) {
    const remaining = Math.max(0, Math.ceil((recoveryMs - nowMs) / 1000))
    recoveryLabel =
      remaining > 0
        ? formatSessionWindowCountdown(remaining)
        : t('Waiting for status update')
    recoveryTitle = t(
      'Estimated release: {{time}} (based on the latest request)',
      {
        time: new Date(recoveryMs).toLocaleString(i18n.language),
      }
    )
  }

  return (
    <span className='inline-flex flex-wrap items-center gap-x-2'>
      <span>{`${props.used}/${props.limit}`}</span>
      {props.used > 0 && (
        <span
          className='text-muted-foreground font-normal'
          title={recoveryTitle}
          aria-label={recoveryTitle}
          tabIndex={hasRecovery ? 0 : undefined}
        >
          {t('Next window release')}: {recoveryLabel}
        </span>
      )}
    </span>
  )
}
