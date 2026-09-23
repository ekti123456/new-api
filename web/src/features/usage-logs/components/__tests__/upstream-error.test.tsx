import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import zhCN from '@/i18n/locales/zh.json'

import { UpstreamErrorDetails } from '../upstream-error-details'

const diagnostic = {
  message: 'read tcp: connection reset by peer <script>',
  code: 'internal_error',
  type: 'server_error',
  source: 'transport',
  stage: 'ws_read',
  transport: 'websocket',
  gateway_request_id: 'gateway-request',
}

test('ordinary users cannot see protected errors in either preview or details', () => {
  for (const compact of [true, false]) {
    assert.equal(
      renderToStaticMarkup(
        <UpstreamErrorDetails
          diagnostic={diagnostic}
          isAdmin={false}
          compact={compact}
        />
      ),
      ''
    )
  }
})

test('admin preview retains the concrete cause and escapes provider text', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <UpstreamErrorDetails diagnostic={diagnostic} isAdmin compact />
    </I18nextProvider>
  )
  assert.ok(html.includes('connection reset by peer'))
  assert.ok(html.includes('&lt;script&gt;'))
  assert.ok(!html.includes('<script>'))
})

test('Chinese admin details distinguish unobserved status from a real upstream 500', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'zhCN', resources: { zhCN } })
  const render = (httpStatus?: number) =>
    renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <UpstreamErrorDetails
          diagnostic={{ ...diagnostic, http_status: httpStatus }}
          isAdmin
        />
      </I18nextProvider>
    )
  assert.ok(render().includes('未观测到'))
  assert.ok(!render().includes('>500<'))
  assert.ok(render(500).includes('>500<'))
  assert.ok(render(500).includes('上游错误详情（脱敏）'))
  assert.ok(render(500).includes('gateway-request'))
})

test('historical logs without diagnostics do not invent an original error', () => {
  assert.equal(renderToStaticMarkup(<UpstreamErrorDetails isAdmin />), '')
})
