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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

import type { ApiKey } from '../../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { ApiKeysProvider } = await import('../api-keys-provider')
const { ApiKeysMutateDrawer } = await import('../api-keys-mutate-drawer')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = {
  get: ApiMethod
  post: ApiMethod
  put: ApiMethod
}
type RenderedDrawer = {
  queryClient: InstanceType<typeof QueryClient>
}

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPost = apiClient.post
const originalPut = apiClient.put
let renderedDrawer: RenderedDrawer | null = null

const storedApiKey: ApiKey = {
  id: 7,
  name: 'mapped',
  key: 'sk-mapped',
  status: 1,
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 1,
  accessed_time: 0,
  group: 'auto',
  auto_groups: null,
  cross_group_retry: true,
  model_limits_enabled: false,
  model_limits: '',
  model_mapping: '{"claude-opus-4-8":"dsv4f"}',
  allow_ips: '',
  profiles: null,
}

const storedProfileApiKey: ApiKey = {
  ...storedApiKey,
  profiles: {
    active_profile: 'dsv4f',
    profiles: [
      {
        name: 'dsv4f',
        model_mapping: { 'claude-opus-4-8': 'dsv4f' },
        model_limits: ['dsv4f'],
        active_route_preset: 'normal',
        route_presets: [
          {
            name: 'normal',
            auto_groups: ['vip'],
            route_keys: [],
            cross_group_retry: true,
          },
        ],
      },
    ],
  },
}

type ApiFixtures = {
  updatedPayloads?: Array<Record<string, unknown>>
  storedRow?: ApiKey
  directRouting?: { enabled: boolean; max_channels: number; allowed: boolean }
  requestedUrls?: string[]
}

function installApiFixtures(
  createdPayloads: Array<Record<string, unknown>>,
  fixtures: ApiFixtures = {}
) {
  const updatedPayloads = fixtures.updatedPayloads ?? []
  apiClient.get = async (url) => {
    fixtures.requestedUrls?.push(url)
    switch (url) {
      case '/api/channel/route_options':
        // Only reachable when the server reports direct routing as allowed;
        // every other case fails loudly instead of hiding a stray request.
        if (!fixtures.directRouting?.allowed) {
          throw new Error('Unexpected route options request')
        }
        return {
          data: {
            success: true,
            data: [
              {
                id: 12,
                route_key: 'ch_AbCdEfGhIjKlMnOp',
                name: 'Azure Prod',
                type: 1,
                status: 1,
                tag: 'prod',
              },
            ],
          },
        }
      case '/api/status':
        return { data: { data: { default_use_auto_group: true } } }
      case '/api/user/models':
        return { data: { success: true, data: [] } }
      case '/api/user/self/groups':
        return {
          data: {
            success: true,
            data: {
              auto: { desc: 'Automatic routing', ratio: 'auto' },
              default: { desc: 'Standard access', ratio: 1 },
              vip: { desc: 'Priority access', ratio: 2 },
            },
          },
        }
      case '/api/token/auto-groups':
        return {
          data: {
            success: true,
            data: {
              groups: ['vip', 'default'],
              max_count: 3,
              ...(fixtures.directRouting
                ? { direct_routing: fixtures.directRouting }
                : {}),
            },
          },
        }
      case '/api/token/7':
        return {
          data: { success: true, data: fixtures.storedRow ?? storedApiKey },
        }
      default:
        throw new Error(`Unexpected GET ${url}`)
    }
  }
  apiClient.post = async (url, data) => {
    expect(url).toBe('/api/token/')
    expect(data && typeof data === 'object').toBeTruthy()
    createdPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
  apiClient.put = async (url, data) => {
    expect(url).toBe('/api/token/')
    expect(data && typeof data === 'object').toBeTruthy()
    updatedPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
}

async function renderDrawer(
  currentRow?: ApiKey,
  directRouting?: { enabled: boolean; max_channels: number; allowed: boolean }
): Promise<void> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(
    ['status'],
    { default_use_auto_group: true },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-models'],
    { success: true, data: [] },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-groups'],
    {
      success: true,
      data: {
        auto: { desc: 'Automatic routing', ratio: 'auto' },
        default: { desc: 'Standard access', ratio: 1 },
        vip: { desc: 'Priority access', ratio: 2 },
      },
    },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['token-auto-groups'],
    {
      success: true,
      data: {
        groups: ['vip', 'default'],
        max_count: 3,
        ...(directRouting ? { direct_routing: directRouting } : {}),
      },
    },
    { updatedAt: freshAt }
  )
  renderedDrawer = { queryClient }

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            onOpenChange={() => undefined}
            currentRow={currentRow}
          />
        </ApiKeysProvider>
      </I18nextProvider>
    </QueryClientProvider>
  )
  await waitFor(
    () => {
      const saveButton = findButton('Save changes', false)
      expect(saveButton).toBeEnabled()
    },
    { timeout: 1500 }
  )
}

function findButton(text: string, required: true): HTMLButtonElement
function findButton(text: string, required: false): HTMLButtonElement | null
function findButton(text: string, required = true): HTMLButtonElement | null {
  const button = screen
    .queryAllByRole<HTMLButtonElement>('button')
    .find((candidate) => candidate.textContent?.includes(text))
  if (required && !button) {
    throw new Error(`Expected button containing "${text}"`)
  }
  return button ?? null
}

function getControlByLabel(labelText: 'Name' | 'Quantity'): HTMLInputElement
function getControlByLabel(labelText: 'Group'): HTMLButtonElement
function getControlByLabel(labelText: 'Auto group order'): HTMLElement
function getControlByLabel(labelText: string): HTMLElement {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  if (!label) {
    throw new Error(`Expected label "${labelText}"`)
  }

  const control =
    label.control ??
    label
      .closest('[data-slot="form-item"]')
      ?.querySelector<HTMLElement>(
        '[data-slot="form-control"], input, textarea, button[role="combobox"], [role="group"]'
      )
  if (!control) {
    throw new Error(`Expected control for label "${labelText}"`)
  }
  return control
}

function changeInput(input: HTMLInputElement, value: string): void {
  fireEvent.input(input, { target: { value } })
}

function selectComboboxOption(
  trigger: HTMLButtonElement,
  optionDescription: string
): void {
  fireEvent.click(trigger)
  const option = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].find((candidate) => candidate.textContent?.includes(optionDescription))
  if (!option) {
    throw new Error(`Expected option containing "${optionDescription}"`)
  }
  fireEvent.click(option)
}

afterEach(() => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  apiClient.put = originalPut
  localStorage.clear()
  if (renderedDrawer) {
    renderedDrawer.queryClient.clear()
    renderedDrawer = null
  }
})

describe('API keys mutate drawer Auto group integration', () => {
  test('inherits the root Auto order and sends an empty override for every batch-created key', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    const groupTrigger = getControlByLabel('Group')
    expect(groupTrigger.textContent?.includes('auto')).toBe(true)
    expect(
      document.body.textContent?.includes(
        'Using the complete global Auto order (2 groups)'
      )
    ).toBe(true)
    expect(
      [
        ...document.querySelectorAll('[data-slot="global-auto-order-name"]'),
      ].map((item) => item.textContent)
    ).toEqual(['vip', 'default'])
    expect(findButton('Restore global Auto', true).disabled).toBe(true)

    changeInput(getControlByLabel('Name'), 'batch')
    changeInput(getControlByLabel('Quantity'), '2')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(2))

    expect(createdPayloads.length).toBe(2)
    expect(createdPayloads[0]?.name).toBe('batch')
    for (const payload of createdPayloads) {
      expect(payload.group).toBe('auto')
      expect(payload.auto_groups).toEqual([])
      expect(payload.cross_group_retry).toBe(true)
    }
  })

  test('preserves an unsaved custom order and mode after Auto to ordinary to Auto changes', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    const autoOrderControl = getControlByLabel('Auto group order')
    const addGroupTrigger = autoOrderControl.querySelector<HTMLButtonElement>(
      'button[role="combobox"]'
    )
    if (!addGroupTrigger) {
      throw new Error('Expected Auto group order combobox')
    }
    selectComboboxOption(addGroupTrigger, 'Priority access')

    expect(
      document.querySelector('button[aria-label="Remove vip"]')
    ).toBeTruthy()
    expect(document.body.textContent?.includes('1 / 3 groups selected')).toBe(
      true
    )
    expect(findButton('Restore global Auto', true).disabled).toBe(false)

    const groupTrigger = getControlByLabel('Group')
    selectComboboxOption(groupTrigger, 'Standard access')
    expect(document.querySelector('button[aria-label="Remove vip"]')).toBe(null)
    selectComboboxOption(groupTrigger, 'Automatic routing')

    expect(
      document.querySelector('button[aria-label="Remove vip"]')
    ).toBeTruthy()
    expect(document.body.textContent?.includes('1 / 3 groups selected')).toBe(
      true
    )
    expect(findButton('Restore global Auto', true).disabled).toBe(false)

    changeInput(getControlByLabel('Name'), 'custom')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.auto_groups).toEqual(['vip'])
  })
})

describe('API keys mutate drawer model redirect integration', () => {
  function openAdvancedSettings(): void {
    fireEvent.click(findButton('Advanced Settings', true))
  }

  test('creates a key with the redirect entered in the visual editor', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    openAdvancedSettings()
    fireEvent.click(findButton('Add Mapping', true))
    changeInput(
      screen.getByPlaceholderText('claude-opus-4-8'),
      'claude-opus-4-8'
    )
    changeInput(screen.getByPlaceholderText('target-model'), 'dsv4f')
    changeInput(getControlByLabel('Name'), 'redirected')

    fireEvent.click(findButton('Save changes', true))

    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(JSON.parse(String(createdPayloads[0]?.model_mapping))).toEqual({
      'claude-opus-4-8': 'dsv4f',
    })
    expect(createdPayloads[0]?.profiles).toBe(null)
  })

  test('shows a stored redirect when editing and submits it unchanged', async () => {
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], { updatedPayloads })
    await renderDrawer(storedApiKey)

    openAdvancedSettings()

    expect(screen.getByDisplayValue('claude-opus-4-8')).toBeVisible()
    expect(screen.getByDisplayValue('dsv4f')).toBeVisible()

    fireEvent.click(findButton('Save changes', true))

    await waitFor(() => expect(updatedPayloads).toHaveLength(1))
    expect(JSON.parse(String(updatedPayloads[0]?.model_mapping))).toEqual({
      'claude-opus-4-8': 'dsv4f',
    })
    expect(updatedPayloads[0]?.profiles).toBe(null)
  })

  test('keeps an invalid JSON redirect in the drawer and blocks the request', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    openAdvancedSettings()
    fireEvent.click(screen.getByRole('tab', { name: 'JSON' }))
    fireEvent.input(screen.getByRole('textbox', { name: 'Model Redirect' }), {
      target: { value: '{' },
    })
    changeInput(getControlByLabel('Name'), 'broken')

    fireEvent.click(findButton('Save changes', true))

    expect(
      await screen.findByText(
        'Model redirect must be a JSON object with non-empty string keys and values'
      )
    ).toBeVisible()
    expect(createdPayloads).toHaveLength(0)
  })
})

describe('API keys mutate drawer routing profile integration', () => {
  test('keeps the Routing Profiles section collapsed without profiles', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderDrawer()

    expect(screen.queryByRole('button', { name: 'Add profile' })).toBeNull()

    fireEvent.click(findButton('Routing Profiles', true))

    expect(findButton('Add profile', true)).toBeVisible()
  })

  test('opens a stored routing profile document and submits it unchanged', async () => {
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], {
      updatedPayloads,
      storedRow: storedProfileApiKey,
    })
    await renderDrawer(storedProfileApiKey)

    expect(findButton('Add profile', true)).toBeVisible()
    expect(screen.getByLabelText('Profile name')).toHaveValue('dsv4f')
    expect(screen.getByLabelText('Active preset')).toHaveTextContent('normal')

    fireEvent.click(findButton('Save changes', true))

    await waitFor(() => expect(updatedPayloads).toHaveLength(1))
    expect(updatedPayloads[0]?.profiles).toEqual({
      active_profile: 'dsv4f',
      profiles: [
        {
          name: 'dsv4f',
          model_mapping: { 'claude-opus-4-8': 'dsv4f' },
          model_limits: ['dsv4f'],
          active_route_preset: 'normal',
          route_presets: [
            {
              name: 'normal',
              auto_groups: ['vip'],
              cross_group_retry: true,
            },
          ],
        },
      ],
    })
  })

  test('blocks saving when the key group is not auto but profiles keep presets', async () => {
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], {
      updatedPayloads,
      storedRow: { ...storedProfileApiKey, group: 'vip' },
    })
    await renderDrawer({ ...storedProfileApiKey, group: 'vip' })

    expect(
      screen.getByText(
        'Route presets are available when the key group is auto.'
      )
    ).toBeVisible()

    fireEvent.click(findButton('Save changes', true))

    expect(
      await screen.findByText('Route presets require the auto group')
    ).toBeVisible()
    expect(updatedPayloads).toHaveLength(0)
  })

  test('never asks for the channel list when direct routing is unavailable', async () => {
    const requestedUrls: string[] = []
    installApiFixtures([], {
      storedRow: storedProfileApiKey,
      directRouting: { enabled: true, max_channels: 2, allowed: false },
      requestedUrls,
    })
    await renderDrawer(storedProfileApiKey)

    expect(requestedUrls).not.toContain('/api/channel/route_options')
    expect(
      screen.getByRole('button', { name: 'Channels' })
    ).toBeDisabled()
  })

  test('offers the fetched channels for a direct route preset', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    const requestedUrls: string[] = []
    installApiFixtures(createdPayloads, {
      storedRow: storedProfileApiKey,
      directRouting: { enabled: true, max_channels: 2, allowed: true },
      requestedUrls,
    })
    await renderDrawer(
      storedProfileApiKey,
      { enabled: true, max_channels: 2, allowed: true }
    )

    expect(requestedUrls).toContain('/api/channel/route_options')

    const preset = document.querySelector<HTMLElement>(
      '[data-slot="route-preset"]'
    )
    if (!preset) throw new Error('Expected a stored route preset card')
    fireEvent.click(
      within(preset).getByRole('button', { name: 'Channels' })
    )
    fireEvent.click(within(preset).getByRole('combobox'))
    const option = [
      ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
    ].find((candidate) => candidate.textContent?.includes('Azure Prod'))
    if (!option) throw new Error('Expected the fetched channel candidate')
    fireEvent.click(option)

    expect(within(preset).getByText('Azure Prod')).toBeVisible()
    expect(preset).toHaveTextContent('1 / 2 channels selected')
  })
})
