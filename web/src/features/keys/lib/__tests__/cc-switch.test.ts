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
import { test } from 'node:test'

import { buildCCSwitchURL } from '../cc-switch'

test('Codex import selects OpenAI as both provider ID and name', () => {
  const url = new URL(
    buildCCSwitchURL(
      'codex',
      'Custom name',
      { model: 'gpt-6-astra' },
      'sk-test',
      'https://api.example.com'
    )
  )
  assert.equal(url.searchParams.get('codexModelProvider'), 'OpenAI')
  assert.equal(url.searchParams.get('name'), 'OpenAI')
  assert.equal(url.searchParams.get('endpoint'), 'https://api.example.com/v1')
  assert.equal(url.searchParams.get('model'), 'gpt-6-astra')
})

for (const app of ['claude', 'gemini']) {
  test(`${app} import preserves its name, endpoint and model fields`, () => {
    const models: Record<string, string> =
      app === 'claude'
        ? { model: 'claude-sonnet', haikuModel: 'claude-haiku', opusModel: '' }
        : { model: 'gemini-pro' }
    const url = new URL(
      buildCCSwitchURL(
        app,
        `My ${app}`,
        models,
        'sk-test',
        'https://api.example.com'
      )
    )
    assert.deepEqual(Object.fromEntries(url.searchParams), {
      resource: 'provider',
      app,
      name: `My ${app}`,
      endpoint: 'https://api.example.com',
      apiKey: 'sk-test',
      model: models.model,
      ...(app === 'claude' ? { haikuModel: 'claude-haiku' } : {}),
      homepage: 'https://api.example.com',
      enabled: 'true',
    })
  })
}
