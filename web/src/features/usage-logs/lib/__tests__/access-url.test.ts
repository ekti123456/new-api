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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { getAdminAccessURL } from '../format'

describe('admin access URL visibility', () => {
  test('shows a recorded access URL to administrators', () => {
    const other = {
      admin_info: { access_url: 'https://chat.example.com' },
    }

    assert.equal(getAdminAccessURL(other, true), 'https://chat.example.com')
  })

  test('hides a recorded access URL from regular users', () => {
    const other = {
      admin_info: { access_url: 'https://chat.example.com' },
    }

    assert.equal(getAdminAccessURL(other, false), null)
  })

  test('hides empty and invalid access URL values', () => {
    assert.equal(
      getAdminAccessURL({ admin_info: { access_url: '  ' } }, true),
      null
    )
    assert.equal(getAdminAccessURL({}, true), null)
  })
})
