import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Main } from '@/components/layout'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogDescription,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getPersonalWindows, saveExpansion } from './api'
import { WindowPolicyEditor } from './policy-editor'
import { WindowPoolCard } from './window-pool-card'

export function PersonalWindows(): React.JSX.Element {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['personal-windows', user?.id],
    queryFn: getPersonalWindows,
    staleTime: 15000,
    refetchInterval: 30000,
    refetchIntervalInBackground: false,
  })
  const [confirm, setConfirm] = useState(false)
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const timer = setInterval(() => {
      if (!document.hidden) setNow(Date.now())
    }, 1000)
    return () => clearInterval(timer)
  }, [])
  const mutation = useMutation({
    mutationFn: (enabled: boolean) =>
      saveExpansion(enabled, query.data?.policy.multiplier || 1),
    onSuccess: () => {
      setConfirm(false)
      return client.invalidateQueries({ queryKey: ['personal-windows'] })
    },
  })
  const data = query.data
  const offset = data ? Date.parse(data.server_now) - query.dataUpdatedAt : 0
  return (
    <Main>
      <div className='min-h-0 flex-1 space-y-4 overflow-auto p-4 sm:p-6'>
        <header className='flex flex-wrap items-center justify-between gap-3'>
          <h1 className='text-xl font-semibold'>{t('Window management')}</h1>
          <Button
            variant='outline'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
        </header>
        {query.isPending && <p role='status'>{t('Loading...')}</p>}
        {query.isError && (
          <p role='alert' className='text-destructive'>
            {t('Window service unavailable')} ·{' '}
            {t('Displayed data may be stale')}
          </p>
        )}
        {data && (
          <>
            <section className='space-y-3 rounded-xl border p-4'>
              <label className='flex items-center gap-2 font-medium'>
                <input
                  type='checkbox'
                  role='switch'
                  checked={data.enabled}
                  disabled={
                    mutation.isPending ||
                    (!data.policy.enabled && !data.enabled)
                  }
                  onChange={(event) => {
                    if (event.target.checked) setConfirm(true)
                    else mutation.mutate(false)
                  }}
                />
                {t('Enable expanded windows')}
              </label>
              <p>
                {t('Additional windows')}: +{data.policy.extra_limit} ·{' '}
                {t('Expansion multiplier')}: ×{data.policy.multiplier}
              </p>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Standard windows are used first. Only additional windows cost more. Existing window prices stay fixed until expiry, including after disabling expansion. Cooldown and account restrictions still apply.'
                )}
              </p>
              {!data.policy.enabled && (
                <p>{t('Expansion is currently unavailable')}</p>
              )}
              {data.enabled &&
                data.accepted_multiplier < data.policy.multiplier && (
                  <Button
                    onClick={() => setConfirm(true)}
                    disabled={!data.policy.enabled}
                  >
                    {t('Confirm updated expansion price')}
                  </Button>
                )}
              {mutation.isError && (
                <p role='alert' className='text-destructive'>
                  {mutation.error.message}
                </p>
              )}
            </section>
            <p className='text-muted-foreground text-sm'>
              {t('Last updated')}: {new Date(data.updated_at).toLocaleString()}{' '}
              · {t('Independent service pools have separate limits')}
            </p>
            {data.pools.length === 0 && (
              <p>{t('No window services configured')}</p>
            )}
            {data.pools.map((pool) => (
              <WindowPoolCard key={pool.id} pool={pool} now={now + offset} />
            ))}
            {(user?.role || 0) >= ROLE.ADMIN && (
              <WindowPolicyEditor
                key={JSON.stringify(data.policy)}
                policy={data.policy}
              />
            )}
            <AlertDialog open={confirm} onOpenChange={setConfirm}>
              <AlertDialogContent>
                <AlertDialogTitle>
                  {t('Confirm expansion charges')}
                </AlertDialogTitle>
                <AlertDialogDescription>
                  {t('Only additional windows will be billed at')} ×
                  {data.policy.multiplier}.{' '}
                  {t('Existing window prices remain unchanged.')}
                </AlertDialogDescription>
                {mutation.isError && (
                  <p role='alert'>{mutation.error.message}</p>
                )}
                <Button
                  disabled={mutation.isPending}
                  onClick={() => mutation.mutate(true)}
                >
                  {t('Confirm')}
                </Button>
                <Button
                  variant='outline'
                  disabled={mutation.isPending}
                  onClick={() => setConfirm(false)}
                >
                  {t('Cancel')}
                </Button>
              </AlertDialogContent>
            </AlertDialog>
          </>
        )}
      </div>
    </Main>
  )
}
