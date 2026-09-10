import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'

import { upgradeWindow } from './api'
import type { PersonalWindow } from './types'

export function WindowUpgradeButton(props: {
  window: PersonalWindow
  poolReference: string
  multiplier: number
  enabled: boolean
}): React.JSX.Element {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [confirmation, setConfirmation] = useState<{
    grantID: string
    multiplier: number
    expiresAt: string
  } | null>(null)
  const mutation = useMutation({
    mutationFn: upgradeWindow,
    onSuccess: () => {
      setConfirmation(null)
      return client.invalidateQueries({ queryKey: ['personal-windows'] })
    },
  })
  const changed =
    confirmation !== null &&
    (confirmation.grantID !== props.window.grant_id ||
      confirmation.multiplier !== props.multiplier ||
      confirmation.expiresAt !== props.window.expires_at)
  return (
    <>
      <Button
        size='sm'
        variant='outline'
        disabled={!props.enabled || mutation.isPending}
        onClick={() => {
          mutation.reset()
          setConfirmation({
            grantID: props.window.grant_id || '',
            multiplier: props.multiplier,
            expiresAt: props.window.expires_at,
          })
        }}
      >
        {props.enabled
          ? t('Expand this window on the same account')
          : t('Enable expansion first')}
      </Button>
      <AlertDialog
        open={confirmation !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setConfirmation(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('Confirm this window upgrade')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'After the reserved slot is secured, subsequent requests use the new multiplier. In-flight and past requests keep their original price.'
              )}{' '}
              ×{confirmation?.multiplier}
              <br />
              {t(
                'One expansion allowance is used. The account and expiry stay unchanged.'
              )}{' '}
              {confirmation &&
                new Date(confirmation.expiresAt).toLocaleString()}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {changed && (
            <p role='alert'>
              {t('Window or price changed. Close and confirm again.')}
            </p>
          )}
          {mutation.isError && <p role='alert'>{mutation.error.message}</p>}
          <AlertDialogFooter>
            <Button
              variant='outline'
              disabled={mutation.isPending}
              onClick={() => setConfirmation(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              disabled={mutation.isPending || changed || !props.enabled}
              onClick={() => {
                if (!confirmation || changed) return
                mutation.mutate({
                  pool_reference: props.poolReference,
                  root: props.window.id,
                  grant_id: confirmation.grantID,
                  accepted_multiplier: confirmation.multiplier,
                })
              }}
            >
              {t('Confirm')}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
