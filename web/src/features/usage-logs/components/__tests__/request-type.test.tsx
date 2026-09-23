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

import { RequestTypeBadge } from '../request-type-badge'

test('a related request shows its original independent classification and protocol source', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <RequestTypeBadge classification={{ type: 'related_internal', ingress_type: 'independent_internal', thread_source: 'guardian_review', subagent_kind: 'guardian', related: true }} />
    </I18nextProvider>
  )
  assert.ok(html.includes('Related background'))
  assert.ok(html.includes('Independent background'))
  assert.ok(html.includes('guardian_review'))
})

test('historical logs without recorded classification display not recorded', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  const html = renderToStaticMarkup(<I18nextProvider i18n={i18n}><RequestTypeBadge /></I18nextProvider>)
  assert.ok(html.includes('Not recorded'))
  assert.ok(!html.includes('User request'))
})

test('compaction labels follow the selected language and escape source text', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'zh', resources: { zh: { translation: { Compaction: '压缩' } } } })
  const html = renderToStaticMarkup(<I18nextProvider i18n={i18n}><RequestTypeBadge classification={{ type: 'compaction', ingress_type: 'compaction', thread_source: '<img src=x>', related: false }} /></I18nextProvider>)
  assert.ok(html.includes('压缩'))
  assert.ok(!html.includes('<img src=x>'))
  assert.ok(html.includes('&lt;img'))
})
