import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import zh from '@/i18n/locales/zh.json'

import { BillingRequestDetails } from '../billing-request-details'

test('admin can distinguish fast, missing tier and unread body when priority does not match', async () => {
  const translations = createInstance()
  await translations
    .use(initReactI18next)
    .init({ lng: 'zh', resources: { zh } })
  for (const sample of [
    { field: { state: 'string', value: 'fast' }, expected: '&quot;fast&quot;' },
    { field: { state: 'absent' }, expected: '未传此字段' },
    { field: { state: 'unavailable' }, expected: '计费请求体未读取' },
    { field: { state: 'string', value: '' }, expected: '&quot;&quot;' },
  ]) {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={translations}>
        <BillingRequestDetails
          isAdmin
          diagnostic={{
            source: 'incoming_json',
            content_type: 'application/json',
            body_state: 'json',
            service_tier: sample.field,
          }}
        />
      </I18nextProvider>
    )
    assert.ok(html.includes(sample.expected), sample.expected)
    assert.ok(html.includes('入站计费参数'))
  }
})

test('old records do not invent an inbound value and non-admin viewers cannot see diagnostics', () => {
  assert.equal(renderToStaticMarkup(<BillingRequestDetails isAdmin />), '')
  assert.equal(
    renderToStaticMarkup(
      <BillingRequestDetails
        isAdmin={false}
        diagnostic={{
          source: 'incoming_json',
          content_type: 'application/json',
          body_state: 'json',
          service_tier: { state: 'string', value: 'priority' },
        }}
      />
    ),
    ''
  )
})

test('default upstream priority is displayed separately without claiming a billing match', async () => {
  const translations = createInstance()
  await translations
    .use(initReactI18next)
    .init({ lng: 'zh', resources: { zh } })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={translations}>
      <BillingRequestDetails
        isAdmin
        diagnostic={{
          source: 'incoming_json',
          content_type: 'application/json',
          body_state: 'json',
          service_tier: { state: 'absent' },
          codex2api: {
            service_tier: 'priority',
            source: 'upstream_response',
            actual_service_tier: 'priority',
            local_billing_service_tier: 'default',
            protocol: 'codex2api_billing_v1',
          },
          priority_match_source: 'none',
        }}
      />
    </I18nextProvider>
  )
  assert.ok(html.includes('未传此字段'))
  assert.ok(!html.includes('最终计费匹配档位'))
  assert.ok(html.includes('codex2api 执行档位'))
  assert.ok(html.includes('codex2api 本地计费档位'))
  assert.ok(html.includes('default'))
  assert.ok(html.includes('未匹配 Priority'))
  assert.ok(html.includes('上游默认 Fast 不会触发加价'))
})
