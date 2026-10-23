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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { SettingsCard } from '../components/settings-card'
import {
  getPolicyIPBlocks,
  getPolicyRestrictions,
  releasePolicyRestrictions,
  type PolicyIPBlock,
  type PolicyRelease,
} from './codex-audit-api'

function displayTime(seconds: number) {
  return seconds > 0 ? new Date(seconds * 1000).toLocaleString() : '—'
}

export function PolicyIPManagement() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [ip, setIP] = useState('')
  const [userID, setUserID] = useState('')
  const [status, setStatus] = useState('active')
  const [filters, setFilters] = useState({
    ip: '',
    user_id: '',
    status: 'active',
  })
  const [page, setPage] = useState(1)
  const [selection, setSelection] = useState<Record<string, string> | null>(
    null
  )
  const [release, setRelease] = useState<PolicyRelease | null>(null)
  const list = useQuery({
    queryKey: ['codex-policy', 'ips', filters, page],
    queryFn: () => getPolicyIPBlocks({ ...filters, page: String(page) }),
    enabled: open,
    retry: false,
    refetchOnWindowFocus: false,
  })
  const details = useQuery({
    queryKey: ['codex-policy', 'details', selection],
    queryFn: () => getPolicyRestrictions(selection ?? {}),
    enabled: open && selection !== null,
    retry: false,
    refetchOnWindowFocus: false,
  })
  const save = useMutation({
    mutationFn: releasePolicyRestrictions,
    onSuccess: () => {
      setRelease(null)
      toast.success(
        t('Restrictions released. Login rate limits are unchanged.')
      )
      void queryClient.invalidateQueries({ queryKey: ['codex-policy'] })
    },
  })
  const beginIPRelease = (row: PolicyIPBlock) => {
    save.reset()
    setRelease({
      user_id: row.user_id,
      ips: [{ ip: row.ip, decision_id: row.decision_id }],
      restore_account: false,
      reset_strikes: false,
      reason: '',
    })
  }
  const beginRelatedRelease = () => {
    const user = details.data?.user
    if (!user) return
    save.reset()
    setRelease({
      user_id: user.user_id,
      state_revision: user.state.revision,
      user_auth_version: user.user_auth_version,
      ips: user.active_ips.map((row) => ({
        ip: row.ip,
        decision_id: row.decision_id,
      })),
      restore_account: user.policy_disabled,
      reset_strikes: false,
      reason: '',
    })
  }
  const blockStatus = (row: PolicyIPBlock) => {
    if (row.revoked_at) return t('Revoked')
    if (row.expires_at <= (list.data?.server_time ?? 0)) return t('Expired')
    if (!list.data?.enforcement.enabled) return t('Enforcement paused')
    return t('Active')
  }
  const user = details.data?.user
  const limiter = details.data?.login_limit
  const checkedBlock = details.data?.ip_block
  const checkedIPActive = Boolean(
    checkedBlock?.ip &&
    !checkedBlock.revoked_at &&
    checkedBlock.expires_at > (details.data?.server_time ?? 0)
  )
  return (
    <SettingsCard
      title={t('IP ban management')}
      description={t(
        'Review IP bans, account status and temporary login limits separately. Disabling enforcement retains ban records.'
      )}
    >
      <div className='space-y-4'>
        <Button
          type='button'
          variant='outline'
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {t(open ? 'Hide IP bans' : 'Manage IP bans')}
        </Button>
        {open && (
          <>
            <form
              className='flex flex-wrap items-end gap-3'
              onSubmit={(event) => {
                event.preventDefault()
                setPage(1)
                setFilters({ ip: ip.trim(), user_id: userID.trim(), status })
              }}
            >
              <Field className='w-64'>
                <FieldLabel htmlFor='policy-ip'>{t('IP address')}</FieldLabel>
                <Input
                  id='policy-ip'
                  value={ip}
                  onChange={(event) => setIP(event.target.value)}
                  placeholder={t('Exact IPv4 or IPv6 address')}
                />
              </Field>
              <Field className='w-36'>
                <FieldLabel htmlFor='policy-user'>{t('User ID')}</FieldLabel>
                <Input
                  id='policy-user'
                  type='number'
                  min={1}
                  value={userID}
                  onChange={(event) => setUserID(event.target.value)}
                />
              </Field>
              <Field className='w-auto'>
                <FieldLabel htmlFor='policy-status'>{t('Status')}</FieldLabel>
                <NativeSelect
                  id='policy-status'
                  value={status}
                  onChange={(event) => setStatus(event.target.value)}
                >
                  <NativeSelectOption value='active'>
                    {t('Active')}
                  </NativeSelectOption>
                  <NativeSelectOption value='expired'>
                    {t('Expired')}
                  </NativeSelectOption>
                  <NativeSelectOption value='revoked'>
                    {t('Revoked')}
                  </NativeSelectOption>
                  <NativeSelectOption value='all'>
                    {t('All')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
              <Button type='submit'>{t('Search')}</Button>
              <Button
                type='button'
                variant='outline'
                disabled={!ip.trim() && !userID.trim()}
                onClick={() =>
                  setSelection({ ip: ip.trim(), user_id: userID.trim() })
                }
              >
                {t('Diagnose access')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={list.isFetching || details.isFetching}
                onClick={() =>
                  void queryClient.invalidateQueries({
                    queryKey: ['codex-policy'],
                  })
                }
              >
                {t('Refresh')}
              </Button>
            </form>
            {list.isError && (
              <p role='alert' className='text-destructive'>
                {t(list.error.message)}
              </p>
            )}
            {list.isPending && <p role='status'>{t('Loading...')}</p>}
            {list.data && (
              <>
                <p className='text-sm'>
                  {t(
                    list.data.enforcement.enabled
                      ? 'IP enforcement is enabled'
                      : 'IP enforcement is paused'
                  )}
                  {' · '}
                  {t('Configuration source')}:{' '}
                  {t(
                    list.data.enforcement.source === 'database'
                      ? 'Saved settings'
                      : 'Environment variables'
                  )}
                  {' · '}
                  {t('Checked at')}: {displayTime(list.data.server_time)}
                </p>
                <div className='overflow-x-auto'>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('IP address')}</TableHead>
                        <TableHead>{t('User ID')}</TableHead>
                        <TableHead>{t('Reason / Request ID')}</TableHead>
                        <TableHead>{t('Started / Expires')}</TableHead>
                        <TableHead>{t('Status')}</TableHead>
                        <TableHead>{t('Actions')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {list.data.items.map((row) => (
                        <TableRow key={row.ip}>
                          <TableCell className='font-mono'>{row.ip}</TableCell>
                          <TableCell>{row.user_id}</TableCell>
                          <TableCell>
                            <div>{row.reason_code || '—'}</div>
                            <div
                              className='max-w-64 truncate font-mono text-xs'
                              title={row.request_id}
                            >
                              {row.request_id || '—'}
                            </div>
                          </TableCell>
                          <TableCell>
                            <div>{displayTime(row.created_at)}</div>
                            <div>{displayTime(row.expires_at)}</div>
                            {!row.revoked_at && (
                              <div>
                                {t('Remaining: {{seconds}}s', {
                                  seconds: Math.max(
                                    0,
                                    row.expires_at - list.data.server_time
                                  ),
                                })}
                              </div>
                            )}
                          </TableCell>
                          <TableCell>
                            <Badge variant='outline'>{blockStatus(row)}</Badge>
                            {row.revoked_at > 0 && (
                              <div className='mt-1 text-xs'>
                                {t('Released by user {{id}}', {
                                  id: row.revoked_by,
                                })}
                                <br />
                                {displayTime(row.revoked_at)}
                                <br />
                                {row.revoke_reason}
                              </div>
                            )}
                          </TableCell>
                          <TableCell>
                            <div className='flex gap-2'>
                              <Button
                                size='sm'
                                type='button'
                                variant='outline'
                                onClick={() =>
                                  setSelection({
                                    ip: row.ip,
                                    user_id: String(row.user_id),
                                  })
                                }
                              >
                                {t('Details')}
                              </Button>
                              {!row.revoked_at &&
                                row.expires_at > list.data.server_time && (
                                  <Button
                                    size='sm'
                                    type='button'
                                    variant='outline'
                                    onClick={() => beginIPRelease(row)}
                                  >
                                    {t('Release this IP')}
                                  </Button>
                                )}
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                      {list.data.items.length === 0 && (
                        <TableRow>
                          <TableCell colSpan={6}>
                            {t('No matching IP bans')}
                          </TableCell>
                        </TableRow>
                      )}
                    </TableBody>
                  </Table>
                </div>
                <div className='flex items-center gap-3'>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={page <= 1 || list.isFetching}
                    onClick={() => setPage(page - 1)}
                  >
                    {t('Previous')}
                  </Button>
                  <span>
                    {t('Page {{page}} · {{total}} records', {
                      page,
                      total: list.data.total,
                    })}
                  </span>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={
                      page * list.data.page_size >= list.data.total ||
                      list.isFetching
                    }
                    onClick={() => setPage(page + 1)}
                  >
                    {t('Next')}
                  </Button>
                </div>
              </>
            )}
            {selection && (
              <div
                className='space-y-3 rounded-lg border p-4'
                aria-live='polite'
              >
                <p className='font-medium'>{t('Access restriction details')}</p>
                {details.isFetching && <p role='status'>{t('Loading...')}</p>}
                {details.isError && (
                  <p role='alert' className='text-destructive'>
                    {t(details.error.message)}
                  </p>
                )}
                {details.data && !details.isError && (
                  <>
                    {details.data.ip && (
                      <p>
                        {t('IP address')}: {details.data.ip} ·{' '}
                        {t('IP enforcement is {{state}}', {
                          state: t(
                            details.data.enforcement.enabled
                              ? 'Enabled'
                              : 'Paused'
                          ),
                        })}
                      </p>
                    )}
                    {details.data.ip && (
                      <p>
                        {t(
                          checkedIPActive
                            ? 'This IP has an active ban'
                            : 'This IP has no active ban'
                        )}
                        {checkedIPActive && checkedBlock && (
                          <>
                            {' '}
                            · {t('Triggering user')}: {checkedBlock.user_id} ·{' '}
                            {t('Remaining: {{seconds}}s', {
                              seconds:
                                checkedBlock.expires_at -
                                details.data.server_time,
                            })}
                          </>
                        )}
                      </p>
                    )}
                    {limiter && (
                      <p>
                        {t('Temporary login limit')}: {limiter.count}/
                        {limiter.limit} ·{' '}
                        {t(limiter.blocked ? 'Limited' : 'Not limited')} ·{' '}
                        {t('Remaining: {{seconds}}s', {
                          seconds: limiter.remaining_seconds,
                        })}{' '}
                        ·{' '}
                        {t(
                          limiter.scope === 'redis'
                            ? 'Shared Redis state'
                            : 'This instance only'
                        )}
                      </p>
                    )}
                    {details.data.login_limit_error && (
                      <p role='alert'>
                        {t(
                          'Login limiter status is unavailable. It must not be treated as unblocked.'
                        )}
                      </p>
                    )}
                    {user && (
                      <>
                        <p>
                          {t('User ID')}: {user.user_id} ·{' '}
                          {user.username || t('Deleted user')} ·{' '}
                          {t(
                            user.user_status === 1
                              ? 'Account enabled'
                              : 'Account disabled or deleted'
                          )}
                        </p>
                        <p>
                          {t('Active policy strikes')}: {user.active_strikes} ·{' '}
                          {t('Active IP bans')}: {user.active_ip_count}
                        </p>
                        {!user.policy_disabled && user.user_status !== 1 && (
                          <p>
                            {t(
                              'Account ownership is unverified or it was disabled separately. Review it in user management.'
                            )}
                          </p>
                        )}
                        <p className='font-mono text-sm break-all'>
                          {user.active_ips.map((row) => row.ip).join(', ') ||
                            '—'}
                        </p>
                        <Button
                          type='button'
                          variant='outline'
                          disabled={
                            details.isFetching ||
                            user.active_ip_count > user.active_ips.length
                          }
                          onClick={beginRelatedRelease}
                        >
                          {t('Release related policy restrictions')}
                        </Button>
                        {user.active_ip_count > user.active_ips.length && (
                          <p>
                            {t(
                              'Too many related IP bans. Release them individually before a combined release.'
                            )}
                          </p>
                        )}
                        <p className='text-muted-foreground text-sm'>
                          {t('Recent restriction history (last 20)')}
                        </p>
                        {user.events.map((event) => (
                          <p key={event.id} className='text-sm break-all'>
                            {displayTime(event.created_at)} · {t(event.action)}{' '}
                            · {event.ip} · {t('Operator')}:{' '}
                            {event.actor_id || t('System')} · {event.reason}
                          </p>
                        ))}
                      </>
                    )}
                    <p className='text-muted-foreground text-sm'>
                      {t(
                        'IP release does not reset temporary login limits. On multiple instances without Redis, check the instance that rejected the login.'
                      )}
                    </p>
                  </>
                )}
              </div>
            )}
          </>
        )}
      </div>
      <ConfirmDialog
        open={release !== null}
        onOpenChange={(value) => {
          if (!value && !save.isPending) setRelease(null)
        }}
        title={t('Confirm restriction release')}
        desc={t(
          'Only the listed restrictions will be changed. Shared-IP users are affected. Audit history is retained and existing sessions are not restored.'
        )}
        confirmText={t('Release restrictions')}
        isLoading={save.isPending}
        disabled={
          !release?.reason.trim() ||
          (!release.ips.length &&
            !release.restore_account &&
            !release.reset_strikes)
        }
        handleConfirm={() => {
          if (release) save.mutate(release)
        }}
      >
        {release && (
          <div className='space-y-3'>
            <p>
              {t('User ID')}: {release.user_id}
            </p>
            <p className='max-h-32 overflow-auto font-mono text-sm break-all'>
              {release.ips.map((row) => row.ip).join(', ') ||
                t('No IP changes')}
            </p>
            <p>
              {t(
                release.restore_account
                  ? 'Restore the account disabled by this policy'
                  : 'Account status will remain unchanged'
              )}
            </p>
            {release.state_revision !== undefined && (
              <Field orientation='horizontal'>
                <Checkbox
                  id='reset-policy-strikes'
                  checked={release.reset_strikes}
                  onCheckedChange={(checked) =>
                    setRelease({ ...release, reset_strikes: checked === true })
                  }
                />
                <FieldLabel htmlFor='reset-policy-strikes'>
                  {t('Reset current policy strikes (retain audit history)')}
                </FieldLabel>
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor='release-reason'>
                {t('Release reason')}
              </FieldLabel>
              <Input
                id='release-reason'
                maxLength={100}
                value={release.reason}
                onChange={(event) =>
                  setRelease({ ...release, reason: event.target.value })
                }
              />
            </Field>
            {save.isError && (
              <p role='alert' className='text-destructive'>
                {t(save.error.message)}
              </p>
            )}
          </div>
        )}
      </ConfirmDialog>
    </SettingsCard>
  )
}
