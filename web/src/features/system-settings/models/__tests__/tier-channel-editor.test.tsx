import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

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
const { TieredPricingEditor } = await import('../tiered-pricing-editor')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

const expression =
  'channel_id == 12 && len > 272000 ? tier("long", p * 10 + c * 45) : tier("base", p * 5 + c * 30)'

test('the visual editor edits channel IDs and explains hidden marketplace tiers', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const changes: string[] = []
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <TieredPricingEditor
          billingExpr={expression}
          requestRuleExpr=''
          onBillingExprChange={(value) => changes.push(value)}
          onRequestRuleExprChange={() => {}}
        />
      </I18nextProvider>
    )
  )
  try {
    const channelInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="Channel ID condition"]'
    )
    assert.ok(channelInput)
    assert.equal(channelInput.value, '12')
    const setter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(channelInput, '15')
      channelInput.dispatchEvent(
        new domWindow.Event('input', { bubbles: true }) as unknown as Event
      )
    })
    assert.ok(changes.at(-1)?.includes('channel_id == 15'))
    assert.ok(
      container.textContent?.includes(
        'Model marketplace hides tiers when channel conditions are used.'
      )
    )
    const conditionButtons = [...container.querySelectorAll('button')].filter(
      (button) => button.textContent?.trim() === 'Add condition'
    )
    assert.equal(conditionButtons.length, 2)
    assert.equal(conditionButtons[1].disabled, true)
    assert.equal(
      container.querySelectorAll<HTMLButtonElement>(
        'button[aria-label="Remove tier"]'
      )[1].disabled,
      true
    )
    assert.equal(
      container.querySelectorAll<HTMLInputElement>('#tier-estimator-channel')
        .length,
      1
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
