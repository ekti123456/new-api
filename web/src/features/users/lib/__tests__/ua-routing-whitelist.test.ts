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

import {
  transformFormDataToPayload,
  transformUserToFormDefaults,
} from '../user-form'

describe('UA routing whitelist user form', () => {
  test('loads the persisted whitelist value and sends it when updating a user', () => {
    const defaults = transformUserToFormDefaults({
      id: 2993,
      username: 'whitelisted-user',
      display_name: 'Whitelisted User',
      quota: 0,
      used_quota: 0,
      request_count: 0,
      group: 'default',
      ua_routing_whitelist: true,
      status: 1,
      role: 1,
    })

    assert.equal(defaults.ua_routing_whitelist, true)

    const payload = transformFormDataToPayload(defaults, 2993)
    assert.equal(payload.ua_routing_whitelist, true)
  })
})
