/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { getOptionValue } from '../../hooks/use-system-options'

describe('Codex unlinked account fallback settings', () => {
  test('uses the safe disabled and 300-second defaults when options are absent', () => {
    const settings = getOptionValue([], {
      codex_unlinked_account_fallback_enabled: false,
      codex_unlinked_account_fallback_seconds: 300,
    })

    assert.deepEqual(settings, {
      codex_unlinked_account_fallback_enabled: false,
      codex_unlinked_account_fallback_seconds: 300,
    })
  })

  test('parses persisted switch and time-window values', () => {
    const settings = getOptionValue(
      [
        { key: 'codex_unlinked_account_fallback_enabled', value: 'true' },
        { key: 'codex_unlinked_account_fallback_seconds', value: '900' },
      ],
      {
        codex_unlinked_account_fallback_enabled: false,
        codex_unlinked_account_fallback_seconds: 300,
      }
    )

    assert.equal(settings.codex_unlinked_account_fallback_enabled, true)
    assert.equal(settings.codex_unlinked_account_fallback_seconds, 900)
  })
})
