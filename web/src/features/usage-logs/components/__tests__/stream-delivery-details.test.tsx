import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import zh from '@/i18n/locales/zh.json'

import { StreamDeliveryDetails } from '../stream-delivery-details'

test('incomplete generation remains visible alongside successful terminal delivery and upstream usage', async () => {
  const translations = createInstance()
  await translations
    .use(initReactI18next)
    .init({ lng: 'zh', resources: { zh } })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={translations}>
      <StreamDeliveryDetails
        delivery={{
          terminal_event: 'response.incomplete',
          response_status: 'incomplete',
          incomplete_reason: 'max_output_tokens',
          terminal_write: 'flushed',
          usage_source: 'upstream',
          client_canceled_at_unix_ms: 1789100000000,
        }}
      />
    </I18nextProvider>
  )
  for (const value of [
    'response.incomplete',
    'max_output_tokens',
    'flushed',
    'upstream',
    '1789100000000',
    '响应状态',
    '不代表客户端已收到',
  ]) {
    assert.ok(html.includes(value), value)
  }
  assert.ok(!html.includes('completed'))
})

test('old logs without delivery details do not invent a successful delivery', () => {
  assert.equal(renderToStaticMarkup(<StreamDeliveryDetails />), '')
})

test('missing usage and write failure are shown without exposing markup', async () => {
  const translations = createInstance()
  await translations.use(initReactI18next).init({ lng: 'en', resources: {} })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={translations}>
      <StreamDeliveryDetails
        delivery={{
          terminal_write: 'failed',
          usage_source: 'missing',
          incomplete_reason: '<script>bad</script>',
        }}
      />
    </I18nextProvider>
  )
  assert.ok(html.includes('failed'))
  assert.ok(html.includes('missing'))
  assert.ok(!html.includes('<script>'))
})
