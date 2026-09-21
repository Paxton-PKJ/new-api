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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test } from 'vitest'

import type { ApiKey } from '../../../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { ApiKeysProvider } = await import('../../api-keys-provider')
const { ProfileSwitchDialog } = await import('../profile-switch-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = { put: ApiMethod }

const apiClient = api as unknown as MockableApi
const originalPut = apiClient.put

const apiKey: ApiKey = {
  id: 7,
  name: 'routed',
  key: 'sk-routed',
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
  model_mapping: '',
  allow_ips: '',
  profiles: {
    active_profile: 'dsv4f',
    profiles: [
      {
        name: 'dsv4f',
        active_route_preset: 'normal',
        route_presets: [
          {
            name: 'normal',
            auto_groups: ['vip'],
            cross_group_retry: true,
          },
          {
            name: 'cheap',
            auto_groups: ['default'],
            cross_group_retry: false,
          },
        ],
      },
      {
        name: 'plain',
        route_presets: [
          {
            name: 'only',
            auto_groups: ['vip'],
            cross_group_retry: false,
          },
        ],
      },
    ],
  },
}

function installApiFixtures(putPayloads: Array<Record<string, unknown>>) {
  apiClient.put = async (url, data) => {
    expect(url).toBe('/api/token/?profile_only=true')
    putPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: apiKey } }
  }
}

function renderDialog(row: ApiKey = apiKey) {
  render(
    <I18nextProvider i18n={i18n}>
      <ApiKeysProvider>
        <ProfileSwitchDialog open onOpenChange={() => undefined} apiKey={row} />
      </ApiKeysProvider>
    </I18nextProvider>
  )
}

afterEach(() => {
  apiClient.put = originalPut
})

describe('API key routing profile quick switch', () => {
  test('opens on the stored selection and saves the active preset', async () => {
    const putPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(putPayloads)
    const user = userEvent.setup()
    renderDialog()

    expect(screen.getByLabelText('Active profile')).toHaveTextContent('dsv4f')
    expect(screen.getByLabelText('Active route preset')).toHaveTextContent(
      'normal'
    )

    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(putPayloads).toHaveLength(1))
    expect(putPayloads[0]).toEqual({
      id: 7,
      active_profile: 'dsv4f',
      active_route_preset: 'normal',
    })
  })

  test('switching the profile selects its own stored route preset', async () => {
    const putPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(putPayloads)
    const user = userEvent.setup()
    renderDialog()

    expect(screen.getByLabelText('Active route preset')).toHaveTextContent(
      'normal'
    )

    await user.click(screen.getByLabelText('Active profile'))
    await user.click(await screen.findByRole('option', { name: 'plain' }))

    // 'plain' stores no active preset, so the previous one must not leak.
    expect(screen.getByLabelText('Active route preset')).toHaveTextContent(
      'None'
    )

    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(putPayloads).toHaveLength(1))
    expect(putPayloads[0]).toEqual({
      id: 7,
      active_profile: 'plain',
      active_route_preset: '',
    })
  })

  test('choosing None clears only the active profile', async () => {
    const putPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(putPayloads)
    const user = userEvent.setup()
    renderDialog()

    await user.click(screen.getByLabelText('Active profile'))
    await user.click(
      await screen.findByRole('option', { name: 'None (use key defaults)' })
    )

    expect(screen.getByLabelText('Active route preset')).toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(putPayloads).toHaveLength(1))
    expect(putPayloads[0]).toEqual({ id: 7, active_profile: '' })
  })

  test('disables the preset selection when no profile is active or has presets', async () => {
    const putPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(putPayloads)
    const user = userEvent.setup()
    renderDialog({
      ...apiKey,
      profiles: {
        profiles: [{ name: 'plain' }],
      },
    })

    expect(screen.getByLabelText('Active profile')).toHaveTextContent(
      'None (use key defaults)'
    )
    expect(screen.getByLabelText('Active route preset')).toBeDisabled()

    await user.click(screen.getByLabelText('Active profile'))
    await user.click(await screen.findByRole('option', { name: 'plain' }))

    expect(screen.getByLabelText('Active route preset')).toBeDisabled()
  })
})
