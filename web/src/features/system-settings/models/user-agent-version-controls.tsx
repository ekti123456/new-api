import type { ReactElement } from 'react'
import { useTranslation } from 'react-i18next'

import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { codexUserAgentFamilies } from './user-agent-version-policy'

type Props = {
  enabled: boolean
  minimumVersions: Record<string, string>
  onEnabledChange: (enabled: boolean) => void
  onVersionsChange: (versions: Record<string, string>) => void
}

export function UserAgentVersionControls(props: Props): ReactElement {
  const { t } = useTranslation()
  return (
    <div className='space-y-3 rounded-md border p-3'>
      <h3 className='text-sm font-semibold'>{t('Minimum client versions')}</h3>
      <label className='flex items-center gap-2 text-sm'>
        <Checkbox
          checked={props.enabled}
          onCheckedChange={(checked) => props.onEnabledChange(checked === true)}
        />
        {t('Enable minimum version requirements')}
      </label>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Version requirements apply to all groups, independently of UA routing and its whitelist. Unknown clients are unaffected; matching clients without a valid version are rejected.'
        )}
      </p>
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
        {codexUserAgentFamilies.map((family) => (
          <div key={family} className='grid gap-1.5'>
            <Label htmlFor={`ua-version-${family.replaceAll(' ', '-')}`}>
              {family}
            </Label>
            <Input
              id={`ua-version-${family.replaceAll(' ', '-')}`}
              aria-label={t('Minimum version for {{client}}', {
                client: family,
              })}
              placeholder='0.153.0'
              maxLength={29}
              value={props.minimumVersions[family] || ''}
              disabled={!props.enabled}
              onChange={(event) =>
                props.onVersionsChange({
                  ...props.minimumVersions,
                  [family]: event.target.value,
                })
              }
            />
          </div>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Leave blank for no minimum. Use major.minor.patch. Older clients receive HTTP 400 with an update reminder.'
        )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Codex background and subagent requests using gpt-5.4 are blocked independently of this switch. Normal user requests are unchanged; models are never substituted.'
        )}
      </p>
    </div>
  )
}
