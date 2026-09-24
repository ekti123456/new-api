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

for (const app of ['codex', 'claude', 'gemini']) {
  test(`${app} import replaces the default localhost address with the visited site`, () => {
    const url = new URL(
      buildCCSwitchURL(
        app,
        `My ${app}`,
        { model: 'test-model' },
        'sk-test',
        'http://localhost:3000',
        'https://gateway.example.com'
      )
    )
    assert.equal(
      url.searchParams.get('homepage'),
      'https://gateway.example.com'
    )
    assert.equal(
      url.searchParams.get('endpoint'),
      app === 'codex'
        ? 'https://gateway.example.com/v1'
        : 'https://gateway.example.com'
    )
    assert.equal(
      url.searchParams.get('name'),
      app === 'codex' ? 'OpenAI' : `My ${app}`
    )
  })
}

for (const address of [
  'http://127.0.0.1:3000',
  'http://[::1]:3000',
  'http://0.0.0.0:3000',
  '',
  'not a URL',
  'javascript:alert(1)',
]) {
  test(`Codex import falls back to the visited site for ${address || 'an empty address'}`, () => {
    const url = new URL(
      buildCCSwitchURL(
        'codex',
        'OpenAI',
        {},
        'sk-test',
        address,
        'https://gateway.example.com'
      )
    )
    assert.equal(
      url.searchParams.get('homepage'),
      'https://gateway.example.com'
    )
    assert.equal(
      url.searchParams.get('endpoint'),
      'https://gateway.example.com/v1'
    )
  })
}

test('a configured API domain and path take priority over the visited site without duplicate slashes', () => {
  const url = new URL(
    buildCCSwitchURL(
      'codex',
      'OpenAI',
      {},
      'sk-test',
      ' https://api.example.com/proxy/ ',
      'https://portal.example.com'
    )
  )
  assert.equal(
    url.searchParams.get('homepage'),
    'https://api.example.com/proxy'
  )
  assert.equal(
    url.searchParams.get('endpoint'),
    'https://api.example.com/proxy/v1'
  )
})

test('local deployments preserve an explicitly configured loopback server', () => {
  const url = new URL(
    buildCCSwitchURL(
      'codex',
      'OpenAI',
      {},
      'sk-test',
      'http://localhost:3000',
      'http://localhost:5173'
    )
  )
  assert.equal(url.searchParams.get('homepage'), 'http://localhost:3000')
  assert.equal(url.searchParams.get('endpoint'), 'http://localhost:3000/v1')
})
