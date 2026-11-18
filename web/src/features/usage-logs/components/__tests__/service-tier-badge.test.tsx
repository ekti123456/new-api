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
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { LogOtherData } from '../../types'
import { ServiceTierBadge } from '../service-tier-badge'

function other(value?: string): LogOtherData {
  return {
    admin_info: {
      billing_request: {
        source: 'incoming_json',
        body_state: 'json',
        content_type: 'application/json',
        service_tier:
          value === undefined
            ? { state: 'absent' }
            : { state: 'string', value },
      },
    },
  }
}

describe('service tier below the model in common usage logs', () => {
  it.each([
    ['fast', 'Fast'],
    ['priority', 'Fast'],
    ['ultrafast', 'Ultrafast'],
  ])('shows %s under the model from the original request', (value, label) => {
    render(<ServiceTierBadge other={other(value)} isAdmin />)
    const tier = screen.getByText(label)
    expect(tier).toHaveAttribute('title', `Request service tier: ${value}`)
  })

  it('does not turn upstream default Fast into a user-requested acceleration', () => {
    const data = other()
    const diagnostic = data.admin_info?.billing_request
    if (!diagnostic) throw new Error('Missing request fixture')
    diagnostic.codex2api = {
      service_tier: 'priority',
      source: 'upstream_response',
      protocol: 'codex2api_billing_v1',
    }
    render(<ServiceTierBadge other={data} isAdmin />)
    expect(screen.queryByText('Fast')).not.toBeInTheDocument()
  })

  it('uses a recorded matching rule for user logs without exposing admin diagnostics', () => {
    const data = other('ultrafast')
    data.request_rule_matches = [
      {
        expression: 'param("service_tier") == "priority" ? 2 : 1',
        matched: true,
      },
    ]
    render(<ServiceTierBadge other={data} isAdmin={false} />)
    expect(screen.getByText('Fast')).toHaveAttribute(
      'title',
      'Matched billing tier: priority'
    )
    expect(screen.queryByText('Ultrafast')).not.toBeInTheDocument()
  })

  it('leaves unknown historic records and nonmatching rules unlabelled', () => {
    render(
      <ServiceTierBadge
        other={{
          request_rule_matches: [
            {
              expression: 'param("service_tier") == "fast" ? 2 : 1',
              matched: false,
            },
          ],
        }}
      />
    )
    expect(screen.queryByText('Fast')).not.toBeInTheDocument()
  })
})
