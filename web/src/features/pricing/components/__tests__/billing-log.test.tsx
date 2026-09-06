import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { DynamicPricingBreakdown } from '../dynamic-pricing-breakdown'

const domWindow = new Window()
after(() => domWindow.close())
const translations = createInstance()
await translations.use(initReactI18next).init({ lng: 'en', resources: {} })
const expression =
  '(len > 272000 && channel_id == 42 ? tier("272k", p * 20 + c * 75) : tier("gpt", p * 10 + c * 50)) * (param("service_tier") == "priority" ? 2 : 1) * (param("model") == "gpt-6-astra" ? 1.6 : 1)'

test('log multiplier badges reflect recorded matches rather than configured multipliers', () => {
  const content = domWindow.document.createElement('div')
  content.innerHTML = renderToStaticMarkup(
    <I18nextProvider i18n={translations}>
      <DynamicPricingBreakdown
        compact
        billingExpr={expression.replace(' ? 1.6 : 1)', ' ? 1.60 : 1)')}
        matchedTierLabel='gpt'
        requestRuleMatches={[
          {
            expression: 'param("model") == "gpt-6-astra" ? 1.6 : 1',
            matched: true,
          },
          {
            expression: 'param("service_tier") == "priority" ? 2 : 1',
            matched: false,
          },
        ]}
      />
    </I18nextProvider>
  )
  const rules = [...content.querySelectorAll('li')]
  assert.equal(rules.length, 2)
  assert.ok(rules[0].textContent.includes('Not matched'))
  assert.ok(rules[0].textContent.includes('2x'))
  assert.ok(rules[1].textContent.includes('Matched'))
  assert.ok(rules[1].textContent.includes('1.60x'))
})

test('old log rules without recorded outcomes stay unknown instead of claiming a match', () => {
  const content = domWindow.document.createElement('div')
  content.innerHTML = renderToStaticMarkup(
    <I18nextProvider i18n={translations}>
      <DynamicPricingBreakdown compact billingExpr={expression} />
    </I18nextProvider>
  )
  const rules = [...content.querySelectorAll('li')]
  assert.equal(rules.length, 2)
  for (const rule of rules) {
    assert.ok(rule.textContent.includes('Match status not recorded'))
    assert.equal(rule.textContent.includes('Matched'), false)
    assert.equal(rule.textContent.includes('Not matched'), false)
  }
})

test('log pricing shows only the recorded tier on desktop and mobile, including when the long tier was charged', () => {
  for (const scenario of [
    {
      label: 'gpt',
      price: '$10.0000',
      hiddenPrice: '$20.0000',
      hiddenLabel: '272k',
    },
    {
      label: '272k',
      price: '$20.0000',
      hiddenPrice: '$10.0000',
      hiddenLabel: 'gpt',
    },
  ]) {
    const content = domWindow.document.createElement('div')
    content.innerHTML = renderToStaticMarkup(
      <I18nextProvider i18n={translations}>
        <DynamicPricingBreakdown
          compact
          billingExpr={expression}
          matchedTierLabel={scenario.label}
          showMatchedTierOnly
        />
      </I18nextProvider>
    )
    assert.equal(content.querySelectorAll('tbody tr').length, 1)
    assert.ok(content.textContent.includes(scenario.price))
    assert.equal(content.textContent.includes(scenario.hiddenPrice), false)
    const table = content.querySelector('table')
    assert.ok(table)
    assert.equal(table.textContent.includes(scenario.hiddenLabel), false)
    assert.equal(content.textContent.match(/Matched/g)?.length, 2)
  }
})

test('a log with a missing tier label does not guess the fallback tier or expose the full expression', () => {
  const content = domWindow.document.createElement('div')
  content.innerHTML = renderToStaticMarkup(
    <I18nextProvider i18n={translations}>
      <DynamicPricingBreakdown
        compact
        billingExpr={expression}
        matchedTierLabel='removed'
        showMatchedTierOnly
      />
    </I18nextProvider>
  )
  assert.equal(content.querySelectorAll('tbody tr').length, 0)
  assert.ok(content.textContent.includes('Matched tier unavailable'))
  assert.equal(content.textContent.includes('272k'), false)
  assert.equal(content.textContent.includes('Raw expression'), false)
})
