import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import {
  parseMinimumVersions,
  validMinimumVersions,
} from '../user-agent-version-policy'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  writable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { UserAgentVersionControls } =
  await import('../user-agent-version-controls')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

test('minimum versions restore blank fields and reject malformed release numbers', () => {
  assert.deepEqual(parseMinimumVersions('{"Codex Desktop":"0.153.0"}'), {
    'Codex Desktop': '0.153.0',
  })
  for (const raw of ['null', '[]', 'invalid', '{"codex-tui":153}']) {
    assert.deepEqual(parseMinimumVersions(raw), {})
  }
  assert.equal(
    validMinimumVersions({ 'codex-tui': '', 'Codex Desktop': '0.153.0' }),
    true
  )
  for (const version of [
    '0.153',
    '0.153.0-alpha',
    '-1.2.3',
    '9999999999.0.0',
  ]) {
    assert.equal(validMinimumVersions({ 'codex-tui': version }), false)
  }
})

test('version controls enable per-client fields and keep edits separate from routing', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let enabled = false
  const edits: Record<string, string>[] = []
  const render = async (checked: boolean) =>
    act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <UserAgentVersionControls
            enabled={checked}
            minimumVersions={{ 'Codex Desktop': '0.153.0' }}
            onEnabledChange={(value) => {
              enabled = value
            }}
            onVersionsChange={(versions) => edits.push(versions)}
          />
        </I18nextProvider>
      )
    )
  try {
    await render(false)
    const versionInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="Minimum version for Codex Desktop"]'
    )
    assert.ok(versionInput)
    assert.equal(versionInput.disabled, true)
    assert.equal(versionInput.value, '0.153.0')
    const toggle = container.querySelector<HTMLElement>('[role="checkbox"]')
    assert.ok(toggle)
    await act(async () => toggle.click())
    assert.equal(enabled, true)
    await render(enabled)
    assert.equal(versionInput.disabled, false)
    assert.equal(toggle.getAttribute('aria-checked'), 'true')
    const setter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(versionInput, '0.154.0')
      versionInput.dispatchEvent(
        new domWindow.Event('input', { bubbles: true }) as unknown as Event
      )
    })
    assert.deepEqual(edits.at(-1), { 'Codex Desktop': '0.154.0' })
    assert.ok(container.textContent?.includes('independently of UA routing'))
    assert.ok(container.textContent?.includes('gpt-5.4'))
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
