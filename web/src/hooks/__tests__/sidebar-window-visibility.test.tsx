import assert from 'node:assert/strict'
import { after, beforeEach, test, type TestContext } from 'node:test'

import { Window } from 'happy-dom'

const browser = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: browser[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
  fallbackLng: 'en',
})
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { useSidebarConfig } = await import('../use-sidebar-config')
const { useSidebarData } = await import('../use-sidebar-data')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const { parseSidebarModulesAdmin } =
  await import('@/features/system-settings/maintenance/config')
const { SidebarModulesSection } =
  await import('@/features/system-settings/maintenance/sidebar-modules-section')
const { SettingsPageProvider } =
  await import('@/features/system-settings/components/settings-page-context')
const { SidebarModulesCard } =
  await import('@/features/profile/components/sidebar-modules-card')

beforeEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'sidebar-test',
    role: 1,
    sidebar_modules: '',
    permissions: { sidebar_settings: true },
  })
})
after(() => browser.close())

function PersonalNavigation() {
  const { navGroups } = useSidebarData()
  const groups = useSidebarConfig(navGroups)
  return (
    <nav aria-label='Personal'>
      {groups
        .filter((group) => group.id === 'personal')
        .flatMap((group) => group.items)
        .map((item) => (
          <a key={item.title} href={item.url}>
            {item.title}
          </a>
        ))}
    </nav>
  )
}

function setup(testContext: TestContext, adminConfig = '') {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  client.setQueryData(['status'], { SidebarModulesAdmin: adminConfig })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const previousAdapter = api.defaults.adapter
  testContext.after(async () => {
    await act(async () => root.unmount())
    container.remove()
    client.clear()
    api.defaults.adapter = previousAdapter
    useAuthStore.getState().auth.reset()
    localStorage.clear()
  })
  return { client, container, root }
}

for (const scenario of [
  { name: 'legacy configuration', admin: '', user: '', visible: true },
  {
    name: 'legacy personal configuration without the new key',
    admin: '{"personal":{"enabled":true,"topup":false}}',
    user: '{"personal":{"enabled":true,"topup":false}}',
    visible: true,
  },
  {
    name: 'administrator disables windows despite user enabling it',
    admin: '{"personal":{"enabled":true,"windows":false}}',
    user: '{"personal":{"enabled":true,"windows":true}}',
    visible: false,
  },
  {
    name: 'administrator disables the personal section',
    admin: '{"personal":{"enabled":false}}',
    user: '',
    visible: false,
  },
  {
    name: 'user disables the personal section',
    admin: '',
    user: '{"personal":{"enabled":false}}',
    visible: false,
  },
]) {
  test(`window navigation honors ${scenario.name}`, async (testContext) => {
    const { client, container, root } = setup(testContext, scenario.admin)
    const user = useAuthStore.getState().auth.user
    assert.ok(user)
    useAuthStore.getState().auth.setUser({
      ...user,
      sidebar_modules: scenario.user,
    })
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <PersonalNavigation />
        </QueryClientProvider>
      )
    )
    assert.equal(
      Boolean(container.querySelector('a[href="/windows"]')),
      scenario.visible
    )
  })
}

test('administrator can save the windows toggle and hide only its navigation entry', async (testContext) => {
  const { client, container, root } = setup(testContext)
  const actions = document.createElement('div')
  document.body.append(actions)
  testContext.after(() => actions.remove())
  let savedConfig = ''
  api.defaults.adapter = async (config) => {
    if (config.method === 'put') {
      assert.equal(config.url, '/api/option/')
      const body = JSON.parse(String(config.data))
      assert.equal(body.key, 'SidebarModulesAdmin')
      savedConfig = body.value
    } else {
      assert.equal(config.url, '/api/status')
    }
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data: { SidebarModulesAdmin: savedConfig } },
    }
  }
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <SettingsPageProvider actionsContainer={actions}>
          <SidebarModulesSection
            config={parseSidebarModulesAdmin('')}
            initialSerialized=''
          />
        </SettingsPageProvider>
        <PersonalNavigation />
      </QueryClientProvider>
    )
  )
  const label = [...container.querySelectorAll('label')].find(
    (element) => element.textContent === 'Window management'
  )
  assert.ok(label, 'administrator settings must expose Window management')
  const toggle = container.querySelector(`[id="${label.htmlFor}"]`)
  assert.ok(toggle instanceof HTMLInputElement)
  assert.equal(toggle.checked, true)
  await act(async () => label.click())
  assert.equal(toggle.checked, false)
  const save = [...actions.querySelectorAll('button')].find(
    (button) => button.textContent === 'Save sidebar modules'
  )
  assert.ok(save)
  await act(async () => save.click())
  assert.equal(JSON.parse(savedConfig).personal.windows, false)
  assert.equal(container.querySelector('a[href="/windows"]'), null)
  assert.ok(container.querySelector('a[href="/wallet"]'))
})

test('personal windows preference hides immediately after save and can be restored', async (testContext) => {
  const { client, container, root } = setup(testContext)
  let savedConfig = ''
  api.defaults.adapter = async (config) => {
    assert.equal(config.url, '/api/user/self')
    if (config.method === 'put') {
      savedConfig = JSON.parse(String(config.data)).sidebar_modules
    }
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data: { sidebar_modules: savedConfig } },
    }
  }
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <SidebarModulesCard />
        <PersonalNavigation />
      </QueryClientProvider>
    )
  )
  const toggle = container.querySelector<HTMLElement>(
    '[role="switch"][aria-label="Window management"]'
  )
  assert.ok(toggle, 'personal settings must expose Window management')
  const save = [...container.querySelectorAll('button')].find(
    (button) => button.textContent === 'Save Changes'
  )
  assert.ok(save)
  for (const enabled of [false, true]) {
    await act(async () => toggle.click())
    assert.equal(toggle.getAttribute('aria-checked'), String(enabled))
    await act(async () => save.click())
    assert.equal(JSON.parse(savedConfig).personal.windows, enabled)
    assert.equal(
      Boolean(container.querySelector('a[href="/windows"]')),
      enabled
    )
    assert.ok(container.querySelector('a[href="/wallet"]'))
  }
})
