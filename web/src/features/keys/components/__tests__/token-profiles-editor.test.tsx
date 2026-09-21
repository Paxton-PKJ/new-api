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
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import type { TokenProfileFormValues } from '../../lib'

const { useState } = await import('react')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { TokenProfilesEditor } = await import('../token-profiles-editor')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        '{{count}} / {{max}} groups selected':
          '{{count}} / {{max}} groups selected',
        'Add Auto group': 'Add Auto group',
        'Auto group order': 'Auto group order',
        'Drag {{group}} to reorder': 'Drag {{group}} to reorder',
        'Maximum {{max}} groups selected': 'Maximum {{max}} groups selected',
        'Move {{group}} down': 'Move {{group}} down',
        'Move {{group}} up': 'Move {{group}} up',
        Ratio: 'Ratio',
        'Remove {{group}}': 'Remove {{group}}',
        'Select a group': 'Select a group',
        'Search...': 'Search...',
        'No group found.': 'No group found.',
      },
    },
  },
})

const GROUP_OPTIONS = [
  { value: 'vip', label: 'vip', desc: 'Priority access', ratio: 2 },
  { value: 'default', label: 'default', desc: 'Standard access', ratio: 1 },
]

function profile(
  name: string,
  overrides: Partial<TokenProfileFormValues> = {}
): TokenProfileFormValues {
  return {
    id: `id-${name}`,
    name,
    model_mapping: '',
    model_limits: [],
    active_route_preset: '',
    route_presets: [],
    ...overrides,
  }
}

function Harness(props: {
  initial: TokenProfileFormValues[]
  activeProfile?: string
  groupIsAuto?: boolean
}) {
  const [profiles, setProfiles] = useState(props.initial)
  const [activeProfile, setActiveProfile] = useState(props.activeProfile ?? '')
  return (
    <I18nextProvider i18n={i18n}>
      <TokenProfilesEditor
        value={profiles}
        onChange={setProfiles}
        activeProfile={activeProfile}
        onActiveProfileChange={setActiveProfile}
        models={['dsv4f', 'claude-opus-4-8']}
        groupOptions={GROUP_OPTIONS}
        maxAutoGroups={3}
        groupIsAuto={props.groupIsAuto ?? true}
      />
      <output data-testid='profiles'>{JSON.stringify(profiles)}</output>
      <output data-testid='active-profile'>{activeProfile}</output>
    </I18nextProvider>
  )
}

function readProfiles(container: HTMLElement): TokenProfileFormValues[] {
  return JSON.parse(
    within(container).getByTestId('profiles').textContent ?? '[]'
  ) as TokenProfileFormValues[]
}

function readActiveProfile(container: HTMLElement): string {
  return within(container).getByTestId('active-profile').textContent ?? ''
}

function getPresetBox(container: HTMLElement): HTMLElement {
  const preset = container.querySelector<HTMLElement>(
    '[data-slot="route-preset"]'
  )
  if (!preset) throw new Error('Expected a route preset card')
  return preset
}

function addPreset(): void {
  fireEvent.click(screen.getByRole('button', { name: 'Add preset' }))
}

function selectAutoGroup(preset: HTMLElement, label: string): void {
  fireEvent.click(within(preset).getByRole('combobox'))
  const option = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].find((candidate) => candidate.textContent?.includes(label))
  if (!option) throw new Error(`Expected group option "${label}"`)
  fireEvent.click(option)
}

function renameInput(input: HTMLInputElement, value: string): void {
  fireEvent.input(input, { target: { value } })
}

describe('Token routing profiles editor', () => {
  test('renders every stored profile expanded and marks the active one', () => {
    render(
      <Harness
        initial={[profile('dsv4f'), profile('cheap')]}
        activeProfile='cheap'
      />
    )

    expect(
      screen
        .getAllByLabelText<HTMLInputElement>('Profile name')
        .map((input) => input.value)
    ).toEqual(['dsv4f', 'cheap'])
    expect(screen.getAllByText('Active')).toHaveLength(1)
    expect(
      screen.getByText('Active').closest('[data-slot="status-badge"]')
    ).not.toBe(null)
  })

  test('adds a profile with the next free default name', () => {
    const { container } = render(
      <Harness initial={[profile('profile-1')]} activeProfile='profile-1' />
    )

    fireEvent.click(screen.getByRole('button', { name: 'Add profile' }))

    const profiles = readProfiles(container)
    expect(profiles).toHaveLength(2)
    expect(profiles[1]?.name).toBe('profile-2')
    expect(screen.getByDisplayValue('profile-2')).toBeVisible()
    expect(readActiveProfile(container)).toBe('profile-1')
  })

  test('removing the active profile clears the active selection', () => {
    const { container } = render(
      <Harness
        initial={[profile('dsv4f'), profile('cheap')]}
        activeProfile='cheap'
      />
    )

    fireEvent.click(
      screen.getAllByRole('button', {
        name: 'Remove profile',
      })[1] as HTMLElement
    )

    expect(readProfiles(container).map((item) => item.name)).toEqual(['dsv4f'])
    expect(readActiveProfile(container)).toBe('')
  })

  test('removing another profile keeps the active selection', () => {
    const { container } = render(
      <Harness
        initial={[profile('dsv4f'), profile('cheap')]}
        activeProfile='cheap'
      />
    )

    fireEvent.click(
      screen.getAllByRole('button', {
        name: 'Remove profile',
      })[0] as HTMLElement
    )

    expect(readActiveProfile(container)).toBe('cheap')
  })

  test('renaming the active profile follows the new name', () => {
    const { container } = render(
      <Harness
        initial={[profile('dsv4f'), profile('cheap')]}
        activeProfile='cheap'
      />
    )

    renameInput(
      screen.getAllByLabelText<HTMLInputElement>(
        'Profile name'
      )[1] as HTMLInputElement,
      'cheap-v2'
    )

    expect(readProfiles(container)[1]?.name).toBe('cheap-v2')
    expect(readActiveProfile(container)).toBe('cheap-v2')
  })

  test('hides the route preset editor while the key group is not auto', () => {
    render(<Harness initial={[profile('dsv4f')]} groupIsAuto={false} />)

    expect(
      screen.getByText(
        'Route presets are available when the key group is auto.'
      )
    ).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Add preset' })).toBeNull()
    expect(screen.queryByLabelText('Active preset')).toBeNull()
  })

  test('adds an empty route preset that requires an explicit Auto group', () => {
    const { container } = render(<Harness initial={[profile('dsv4f')]} />)

    addPreset()

    const presets = readProfiles(container)[0]?.route_presets ?? []
    expect(presets).toHaveLength(1)
    expect(presets[0]).toMatchObject({
      name: 'preset-1',
      auto_groups: [],
      cross_group_retry: true,
    })
    expect(
      screen.getByText('Select at least one Auto group for this route preset.')
    ).toBeVisible()
  })

  test('records the groups, retry flag and active preset of a route preset', async () => {
    const user = userEvent.setup()
    const { container } = render(<Harness initial={[profile('dsv4f')]} />)

    addPreset()
    selectAutoGroup(getPresetBox(container), 'vip')
    selectAutoGroup(getPresetBox(container), 'default')
    fireEvent.click(within(getPresetBox(container)).getByRole('switch'))

    const preset = readProfiles(container)[0]?.route_presets?.[0]
    expect(preset?.auto_groups).toEqual(['vip', 'default'])
    expect(preset?.cross_group_retry).toBe(false)

    await user.click(screen.getByLabelText('Active preset'))
    await user.click(await screen.findByRole('option', { name: 'preset-1' }))

    expect(readProfiles(container)[0]?.active_route_preset).toBe('preset-1')
  })

  test('clears the active preset when its route preset is removed', () => {
    const { container } = render(
      <Harness
        initial={[
          profile('dsv4f', {
            active_route_preset: 'preset-1',
            route_presets: [
              {
                id: 'preset-a',
                name: 'preset-1',
                auto_groups: ['vip'],
                cross_group_retry: true,
              },
            ],
          }),
        ]}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: 'Remove preset' }))

    const storedProfile = readProfiles(container)[0]
    expect(storedProfile?.route_presets).toEqual([])
    expect(storedProfile?.active_route_preset).toBe('')
  })
})
