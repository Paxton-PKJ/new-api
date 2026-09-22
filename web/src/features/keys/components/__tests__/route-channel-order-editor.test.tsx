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
import { fireEvent, render, within } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import type { ChannelRouteOption } from '@/features/channels/types'

const { useState } = await import('react')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { RouteChannelOrderEditor } = await import(
  '../route-channel-order-editor'
)

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        '{{count}} / {{max}} channels selected':
          '{{count}} / {{max}} channels selected',
        'Add channel': 'Add channel',
        'Deleted channel': 'Deleted channel',
        Disabled: 'Disabled',
        'Drag {{group}} to reorder': 'Drag {{group}} to reorder',
        Invalid: 'Invalid',
        'Loading…': 'Loading…',
        'Maximum {{max}} channels selected':
          'Maximum {{max}} channels selected',
        'Move {{group}} down': 'Move {{group}} down',
        'Move {{group}} up': 'Move {{group}} up',
        'No channel found.': 'No channel found.',
        'Remove {{group}}': 'Remove {{group}}',
        'Search channels...': 'Search channels...',
        'Select at least one channel for this route preset.':
          'Select at least one channel for this route preset.',
        Channels: 'Channels',
      },
    },
  },
})

const CHANNEL_OPTIONS: ChannelRouteOption[] = [
  {
    id: 12,
    route_key: 'ch_AbCdEfGhIjKlMnOp',
    name: 'Azure Prod',
    type: 1,
    status: 1,
    tag: 'prod',
  },
  {
    id: 13,
    route_key: 'ch_QqRrSsTtUuVvWwXx',
    name: 'Azure Backup',
    type: 1,
    status: 2,
    tag: null,
  },
  {
    id: 40,
    route_key: 'ch_ZzYyXxWwVvUuTtSs',
    name: 'Local Llama',
    type: 1,
    status: 1,
    tag: 'lab',
  },
]

function Harness(props: {
  initial?: string[]
  options?: ChannelRouteOption[]
  optionsLoaded?: boolean
}) {
  const [value, setValue] = useState(props.initial ?? [])
  return (
    <I18nextProvider i18n={i18n}>
      <RouteChannelOrderEditor
        value={value}
        options={props.options ?? CHANNEL_OPTIONS}
        optionsLoaded={props.optionsLoaded ?? true}
        maxCount={2}
        onChange={setValue}
      />
      <output data-testid='order'>{value.join(',')}</output>
    </I18nextProvider>
  )
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement {
  return within(container).getByRole('button', { name: label })
}

/** The picker that is currently open; a closing popover stays in the DOM. */
function openPicker(): HTMLElement {
  const popover = document.querySelector<HTMLElement>(
    '[data-slot="popover-content"][data-open]'
  )
  if (!popover) throw new Error('Expected an open channel picker')
  return popover
}

function searchInput(): HTMLInputElement {
  const input = openPicker().querySelector<HTMLInputElement>(
    '[data-slot="command-input"]'
  )
  if (!input) throw new Error('Expected the channel search input')
  return input
}

function openPickerFor(container: HTMLElement): void {
  fireEvent.click(within(container).getByRole('combobox'))
}

function visibleCandidates(): string[] {
  return [
    ...openPicker().querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].map((item) => item.textContent ?? '')
}

function clickCandidate(label: string): void {
  const item = [
    ...openPicker().querySelectorAll<HTMLElement>(
      '[data-slot="command-item"]'
    ),
  ].find((candidate) => candidate.textContent?.includes(label))
  if (!item) throw new Error(`Expected channel candidate "${label}"`)
  fireEvent.click(item)
}

describe('Route channel order editor', () => {
  test('starts empty and explains what a direct preset needs', () => {
    const { container } = render(<Harness />)

    expect(container).toHaveTextContent('0 / 2 channels selected')
    expect(container).toHaveTextContent(
      'Select at least one channel for this route preset.'
    )
    expect(
      within(container).getByRole('group', { name: 'Channels' })
    ).toBeInTheDocument()
  })

  test('filters candidates by name, channel id and tag', () => {
    const { container } = render(<Harness />)

    openPickerFor(container)
    expect(visibleCandidates()).toHaveLength(3)

    fireEvent.change(searchInput(), { target: { value: 'azure' } })
    expect(visibleCandidates()).toHaveLength(2)

    fireEvent.change(searchInput(), { target: { value: '#13' } })
    expect(visibleCandidates()).toHaveLength(1)
    expect(visibleCandidates()[0]).toContain('Azure Backup')

    fireEvent.change(searchInput(), { target: { value: 'lab' } })
    expect(visibleCandidates()).toHaveLength(1)
    expect(visibleCandidates()[0]).toContain('Local Llama')
  })

  test('reports an empty search result and adds the chosen channel in order', () => {
    const { container } = render(<Harness />)

    openPickerFor(container)
    fireEvent.change(searchInput(), { target: { value: 'nope' } })
    expect(visibleCandidates()).toHaveLength(0)
    expect(openPicker()).toHaveTextContent('No channel found.')

    fireEvent.change(searchInput(), { target: { value: 'azure' } })
    clickCandidate('Azure Prod')
    expect(within(container).getByTestId('order')).toHaveTextContent(
      'ch_AbCdEfGhIjKlMnOp'
    )

    openPickerFor(container)
    // The selected channel leaves the candidate list; the rest stay available.
    expect(visibleCandidates()).toHaveLength(2)
    expect(visibleCandidates().join(' ')).not.toContain('Azure Prod')
    clickCandidate('Azure Backup')
    expect(within(container).getByTestId('order')).toHaveTextContent(
      'ch_AbCdEfGhIjKlMnOp,ch_QqRrSsTtUuVvWwXx'
    )
  })

  test('disables adding at the cap and orders the selected channels', () => {
    const { container } = render(
      <Harness initial={['ch_AbCdEfGhIjKlMnOp', 'ch_QqRrSsTtUuVvWwXx']} />
    )

    const addButton = within(container).getByRole('combobox')
    expect(addButton).toBeDisabled()
    expect(addButton).toHaveTextContent('Maximum 2 channels selected')
    expect(container).toHaveTextContent('2 / 2 channels selected')

    fireEvent.click(findButton(container, 'Move Azure Prod down'))
    expect(within(container).getByTestId('order')).toHaveTextContent(
      'ch_QqRrSsTtUuVvWwXx,ch_AbCdEfGhIjKlMnOp'
    )

    fireEvent.keyDown(findButton(container, 'Drag Azure Prod to reorder'), {
      key: 'ArrowUp',
    })
    expect(within(container).getByTestId('order')).toHaveTextContent(
      'ch_AbCdEfGhIjKlMnOp,ch_QqRrSsTtUuVvWwXx'
    )

    fireEvent.click(findButton(container, 'Remove Azure Prod'))
    expect(within(container).getByTestId('order')).toHaveTextContent(
      'ch_QqRrSsTtUuVvWwXx'
    )
    expect(within(container).getByRole('combobox')).toBeEnabled()
  })

  test('names the reorder controls after the channel instead of its route key', () => {
    const { container } = render(<Harness initial={['ch_AbCdEfGhIjKlMnOp']} />)

    expect(
      findButton(container, 'Drag Azure Prod to reorder')
    ).toBeInTheDocument()
    expect(findButton(container, 'Move Azure Prod up')).toBeDisabled()
    expect(findButton(container, 'Remove Azure Prod')).toBeEnabled()
    expect(container).toHaveTextContent('Azure Prod')
    expect(container).toHaveTextContent('#12')
  })

  test('flags a stored route key whose channel is gone and allows removing it', () => {
    const { container } = render(
      <Harness initial={['ch_MmNnOoPpQqRrSsTt', 'ch_AbCdEfGhIjKlMnOp']} />
    )

    expect(container).toHaveTextContent('Deleted channel')
    expect(container).toHaveTextContent('ch_MmNnOoPpQqRrSsTt')
    expect(container).toHaveTextContent('Invalid')

    fireEvent.click(findButton(container, 'Remove Deleted channel'))
    expect(within(container).getByTestId('order')).toHaveTextContent(
      'ch_AbCdEfGhIjKlMnOp'
    )
    expect(container).not.toHaveTextContent('Invalid')
  })

  test('keeps unknown route keys unflagged until the channel list has loaded', () => {
    const { container } = render(
      <Harness
        initial={['ch_MmNnOoPpQqRrSsTt']}
        optionsLoaded={false}
      />
    )

    expect(container).toHaveTextContent('ch_MmNnOoPpQqRrSsTt')
    expect(container).toHaveTextContent('Loading…')
    expect(container).not.toHaveTextContent('Deleted channel')
    expect(container).not.toHaveTextContent('Invalid')
  })

  test('shows the tag and disabled state of a selected channel', () => {
    const { container } = render(
      <Harness initial={['ch_QqRrSsTtUuVvWwXx', 'ch_ZzYyXxWwVvUuTtSs']} />
    )

    expect(container).toHaveTextContent('Disabled')
    expect(container).toHaveTextContent('lab')
    expect(container).toHaveTextContent('Azure Backup')
  })
})
