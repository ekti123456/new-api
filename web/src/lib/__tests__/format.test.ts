import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  formatSessionWindowCountdown,
  getSessionWindowRemainingSeconds,
} from '../format'

describe('session window countdown formatting', () => {
  test('computes remaining seconds from the window start timestamp', () => {
    const updatedAt = '2026-09-04T00:00:00.000Z'
    const now = Date.parse('2026-09-04T00:12:05.250Z')

    assert.equal(getSessionWindowRemainingSeconds(updatedAt, 900, now), 175)
  })

  test('clamps expired windows to zero and rejects incomplete data', () => {
    const updatedAt = '2026-09-04T00:00:00.000Z'
    const now = Date.parse('2026-09-04T00:15:01.000Z')

    assert.equal(getSessionWindowRemainingSeconds(updatedAt, 900, now), 0)
    assert.equal(getSessionWindowRemainingSeconds(undefined, 900, now), null)
    assert.equal(getSessionWindowRemainingSeconds(updatedAt, 0, now), null)
  })

  test('formats countdown using only the necessary duration units', () => {
    assert.equal(formatSessionWindowCountdown(0), '0s')
    assert.equal(formatSessionWindowCountdown(65), '1m 5s')
    assert.equal(formatSessionWindowCountdown(90061), '1d 1h 1m 1s')
  })
})

