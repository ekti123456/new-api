import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Layers, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Main } from '@/components/layout'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogHeader,
  AlertDialogFooter,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getPersonalWindows, saveExpansion } from './api'
import { ExpansionCard } from './expansion-card'
import { WindowPolicyEditor } from './policy-editor'
import { WindowPoolList } from './window-pool-list'

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
      <div className='min-h-0 flex-1 overflow-auto p-4 sm:p-6'>
        <div className='mx-auto w-full max-w-6xl space-y-6'>
          <header className='flex flex-wrap items-center justify-between gap-3'>
            <div className='space-y-1'>
              <h1 className='text-xl font-semibold tracking-tight'>
                {t('Window management')}
              </h1>
              <p className='text-muted-foreground text-sm'>
                {t('Track your windows, recovery times and expansion charges')}
              </p>
            </div>
            <Button
              variant='outline'
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              <RefreshCw
                className={query.isFetching ? 'size-4 animate-spin' : 'size-4'}
                aria-hidden='true'
              />
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
              <ExpansionCard
                data={data}
                pending={mutation.isPending}
                error={mutation.isError ? mutation.error.message : undefined}
                onChange={(enabled) => {
                  if (enabled) setConfirm(true)
                  else mutation.mutate(false)
                }}
                onConfirmPrice={() => setConfirm(true)}
              />
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <h2 className='flex items-center gap-2 font-semibold'>
                  <Layers
                    className='text-muted-foreground size-4'
                    aria-hidden='true'
                  />
                  {t('Current windows')}
                </h2>
                <p className='text-muted-foreground text-xs'>
                  {t('Last updated')}:{' '}
                  {new Date(data.updated_at).toLocaleString()} ·{' '}
                  {t('Independent service pools have separate limits')}
                </p>
              </div>
              {data.pools.length === 0 && (
                <p className='text-muted-foreground rounded-xl border border-dashed p-8 text-center text-sm'>
                  {t('No window services configured')}
                </p>
              )}
              <WindowPoolList pools={data.pools} offset={offset} />
              {(user?.role || 0) >= ROLE.ADMIN && (
                <WindowPolicyEditor
                  key={JSON.stringify(data.policy)}
                  policy={data.policy}
                />
              )}
              <AlertDialog open={confirm} onOpenChange={setConfirm}>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>
                      {t('Confirm expansion charges')}
                    </AlertDialogTitle>
                    <AlertDialogDescription>
                      {t('Only additional windows will be billed at')} ×
                      {data.policy.multiplier}.{' '}
                      {t('Existing window prices remain unchanged.')}
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  {mutation.isError && (
                    <p role='alert'>{mutation.error.message}</p>
                  )}
                  <AlertDialogFooter>
                    <Button
                      variant='outline'
                      disabled={mutation.isPending}
                      onClick={() => setConfirm(false)}
                    >
                      {t('Cancel')}
                    </Button>
                    <Button
                      disabled={mutation.isPending}
                      onClick={() => mutation.mutate(true)}
                    >
                      {t('Confirm')}
                    </Button>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </>
          )}
        </div>
      </div>
    </Main>
  )
}
