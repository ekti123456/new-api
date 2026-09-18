import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { TimingMetricsCell } from '../timing-metrics-cell'

test('admin hover shows only the actual first frame under the compact first-token label', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <TimingMetricsCell
        useTimeSec={20}
        completionTokens={100}
        frtMs={12000}
        upstreamFirstResponse={{
          source: 'codex2api',
          mode: 'loose',
          ms: 800,
          attempt_ms: 600,
        }}
        isStream
        isAdmin
      />
    </I18nextProvider>
  )
  assert.ok(html.includes('First token'))
  assert.ok(!html.includes('Upstream first response'))
  assert.ok(html.includes('title="Actual first frame: 12.0s"'))
  assert.ok(!html.includes('codex2api'))
  assert.ok(!html.includes('loose'))
  assert.ok(html.includes('0.8s'))
  assert.ok(html.includes('12.0s'))
})

test('regular users and omitted permissions get the same timing without hover details on desktop and mobile', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  for (const isAdmin of [false, undefined]) {
    for (const indicator of ['bar', 'dot'] as const) {
      const html = renderToStaticMarkup(
        <I18nextProvider i18n={i18n}>
          <TimingMetricsCell
            useTimeSec={20}
            completionTokens={100}
            frtMs={12000}
            upstreamFirstResponse={{
              source: 'codex2api',
              mode: 'loose',
              ms: 800,
              attempt_ms: 600,
            }}
            isStream
            isAdmin={isAdmin}
            indicator={indicator}
          />
        </I18nextProvider>
      )
      assert.ok(html.includes('First token'))
      assert.ok(html.includes('0.8s'))
      assert.ok(!html.includes('title='))
      assert.ok(!html.includes('12.0s'))
      assert.ok(!html.includes('codex2api'))
      assert.ok(!html.includes('loose'))
    }
  }
})

test('old logs and invalid reported timing use the observed first frame', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
  for (const ms of [undefined, -1, Infinity, Number.NaN, 15000]) {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <TimingMetricsCell
          useTimeSec={20}
          completionTokens={100}
          frtMs={12000}
          upstreamFirstResponse={
            ms === undefined
              ? undefined
              : { source: 'codex2api', mode: 'loose', ms, attempt_ms: 0 }
          }
          isStream
        />
      </I18nextProvider>
    )
    assert.ok(!html.includes('Upstream first response'))
    assert.ok(html.includes('First token'))
    assert.ok(html.includes('12.0s'))
  }
})
