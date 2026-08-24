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

import { errorRateLockSchema } from '../monitoring-error-rate-lock-schema'

describe('high error-rate temporary lock validation', () => {
  test('accepts the default lock configuration', () => {
    const result = errorRateLockSchema.safeParse({
      user_error_rate_lock_enabled: false,
      user_error_rate_lock_min_requests: 100,
      user_error_rate_lock_threshold: 50,
      user_error_rate_lock_seconds: 60,
    })

    assert.equal(result.success, true)
  })

  test('rejects values outside the server-supported ranges', () => {
    const invalidValues = [
      { minRequests: 0, threshold: 50, seconds: 60 },
      { minRequests: 100, threshold: 0, seconds: 60 },
      { minRequests: 100, threshold: 50, seconds: 86401 },
    ]

    for (const value of invalidValues) {
      const result = errorRateLockSchema.safeParse({
        user_error_rate_lock_enabled: true,
        user_error_rate_lock_min_requests: value.minRequests,
        user_error_rate_lock_threshold: value.threshold,
        user_error_rate_lock_seconds: value.seconds,
      })
      assert.equal(result.success, false)
    }
  })
})
