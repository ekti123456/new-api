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

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { ModelBadge } from '../model-badge'

test('a reported model appears after the requested badge with an arrow, even when names match', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'zh',
    resources: { zh: { translation: { 'Upstream reported': '上游自报' } } },
  })
  for (const responseModel of ['gpt-5.6-luna', 'gpt-6-astra']) {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <ModelBadge modelName='gpt-6-astra' responseModel={responseModel} />
      </I18nextProvider>
    )
    assert.ok(html.includes(`↳ 上游自报: ${responseModel}`))
    assert.ok(html.indexOf('gpt-6-astra') < html.indexOf('↳'))
  }
})

test('absent response models do not fall back to the requested or mapped model', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  for (const responseModel of [undefined, '', '  ']) {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <ModelBadge
          modelName='requested-model'
          actualModel='mapped-model'
          responseModel={responseModel}
        />
      </I18nextProvider>
    )
    assert.ok(html.includes('requested-model'))
    assert.ok(!html.includes('Upstream reported'))
    assert.ok(!html.includes('↳'))
  }
})

test('long and HTML-like reported names remain escaped text with the full value available on hover', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  const responseModel = `<img src=x onerror=alert(1)>${'model'.repeat(30)}`
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModelBadge modelName='requested-model' responseModel={responseModel} />
    </I18nextProvider>
  )
  assert.ok(!html.includes('<img src=x'))
  assert.ok(html.includes('title="Upstream reported: &lt;img'))
  assert.ok(html.includes('model'.repeat(30)))
})
