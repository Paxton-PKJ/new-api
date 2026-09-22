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
import { ChevronsUpDown } from 'lucide-react'
import { Reorder } from 'motion/react'
import { useMemo, useState, type ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import { AutoGroupOrderItem } from '@/components/auto-group-order-item'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import type { ChannelRouteOption } from '@/features/channels/types'
import { cn } from '@/lib/utils'

import { DEFAULT_MAX_ROUTE_PRESET_CHANNELS } from '../lib'

type RouteChannelOrderEditorProps = Omit<ComponentProps<'div'>, 'onChange'> & {
  /** Selected route keys, in attempt order. */
  value: string[]
  options: ChannelRouteOption[]
  /** Whether `options` reflects the server. Unknown keys are not flagged before. */
  optionsLoaded: boolean
  maxCount: number
  disabled?: boolean
  onChange: (routeKeys: string[]) => void
  'aria-label'?: string
  'data-slot'?: string
  'data-form-root'?: string
}

/**
 * Ordered channel picker for a direct route preset. Channels are identified by
 * their stable route key, so a rename keeps the preset and a deleted channel
 * stays visible as an invalid entry the user can clean up.
 */
export function RouteChannelOrderEditor(props: RouteChannelOrderEditorProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [searchValue, setSearchValue] = useState('')
  const maxCount =
    Number.isInteger(props.maxCount) && props.maxCount > 0
      ? props.maxCount
      : DEFAULT_MAX_ROUTE_PRESET_CHANNELS
  const atLimit = props.value.length >= maxCount

  const selectableOptions = useMemo(
    () =>
      props.options.filter(
        (option): option is ChannelRouteOption & { route_key: string } =>
          Boolean(option.route_key)
      ),
    [props.options]
  )
  const optionsByKey = useMemo(
    () => new Map(selectableOptions.map((option) => [option.route_key, option])),
    [selectableOptions]
  )
  const candidates = useMemo(
    () =>
      selectableOptions.filter(
        (option) => !props.value.includes(option.route_key)
      ),
    [selectableOptions, props.value]
  )
  const filteredOptions = useMemo(() => {
    const search = searchValue.trim().toLowerCase()
    if (!search) return candidates
    return candidates.filter((option) =>
      `${option.name} #${option.id} ${option.route_key} ${option.tag ?? ''}`
        .toLowerCase()
        .includes(search)
    )
  }, [candidates, searchValue])

  const handleAdd = (routeKey: string) => {
    if (atLimit || props.value.includes(routeKey)) return
    props.onChange([...props.value, routeKey])
    setOpen(false)
    setSearchValue('')
  }

  const handleRemove = (routeKey: string) => {
    props.onChange(props.value.filter((item) => item !== routeKey))
  }

  const handleMove = (index: number, direction: 'up' | 'down') => {
    const targetIndex = direction === 'up' ? index - 1 : index + 1
    if (targetIndex < 0 || targetIndex >= props.value.length) return
    const next = [...props.value]
    ;[next[index], next[targetIndex]] = [next[targetIndex], next[index]]
    props.onChange(next)
  }

  return (
    <div
      id={props.id}
      data-slot={props['data-slot']}
      data-form-root={props['data-form-root']}
      role='group'
      tabIndex={-1}
      aria-label={props['aria-label'] || t('Channels')}
      aria-describedby={props['aria-describedby']}
      aria-invalid={props['aria-invalid']}
      className={cn('flex flex-col gap-3', props.className)}
    >
      <p className='text-muted-foreground text-xs' aria-live='polite'>
        {t('{{count}} / {{max}} channels selected', {
          count: props.value.length,
          max: maxCount,
        })}
      </p>

      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              type='button'
              variant='outline'
              role='combobox'
              aria-expanded={open}
              disabled={props.disabled || atLimit || candidates.length === 0}
              className='w-full justify-between font-normal'
            />
          }
        >
          <span className='truncate'>
            {atLimit
              ? t('Maximum {{max}} channels selected', { max: maxCount })
              : t('Add channel')}
          </span>
          <ChevronsUpDown
            aria-hidden='true'
            className='size-4 shrink-0 opacity-50'
          />
        </PopoverTrigger>
        <PopoverContent
          className='w-[var(--anchor-width)] overflow-hidden rounded-xl p-0 shadow-lg'
          onWheel={(event) => event.stopPropagation()}
          onTouchMove={(event) => event.stopPropagation()}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <Command shouldFilter={false}>
            <CommandInput
              placeholder={t('Search channels...')}
              value={searchValue}
              onValueChange={setSearchValue}
            />
            <CommandList className='max-h-[360px]'>
              <CommandEmpty>{t('No channel found.')}</CommandEmpty>
              <CommandGroup>
                {filteredOptions.map((option) => (
                  <CommandItem
                    key={option.route_key}
                    value={option.route_key}
                    onSelect={() => handleAdd(option.route_key)}
                    className='items-center gap-2'
                  >
                    <span className='min-w-0 flex-1 truncate font-medium'>
                      {option.name}
                    </span>
                    {option.tag ? (
                      <StatusBadge
                        label={option.tag}
                        variant='neutral'
                        copyable={false}
                        className='shrink-0'
                      />
                    ) : null}
                    {option.status !== 1 ? (
                      <StatusBadge
                        label={t('Disabled')}
                        variant='danger'
                        copyable={false}
                        className='shrink-0'
                      />
                    ) : null}
                    <span className='text-muted-foreground shrink-0 font-mono text-xs'>
                      #{option.id}
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      {props.value.length === 0 && (
        <Empty className='min-h-24 border'>
          <EmptyHeader>
            <EmptyTitle>{t('Channels')}</EmptyTitle>
            <EmptyDescription>
              {t('Select at least one channel for this route preset.')}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}

      {props.value.length > 0 && (
        <Reorder.Group
          axis='y'
          values={props.value}
          onReorder={props.onChange}
          className='flex flex-col gap-2'
        >
          {props.value.map((routeKey, index) => {
            const option = optionsByKey.get(routeKey)
            if (!option) {
              return (
                <AutoGroupOrderItem
                  key={routeKey}
                  group={routeKey}
                  index={index}
                  count={props.value.length}
                  displayName={
                    props.optionsLoaded ? t('Deleted channel') : routeKey
                  }
                  label={
                    props.optionsLoaded ? (
                      <span className='text-muted-foreground'>
                        {t('Deleted channel')}{' '}
                        <span className='font-mono'>{routeKey}</span>
                      </span>
                    ) : (
                      <span className='font-mono'>{routeKey}</span>
                    )
                  }
                  description={
                    <StatusBadge
                      label={props.optionsLoaded ? t('Invalid') : t('Loading…')}
                      variant={props.optionsLoaded ? 'danger' : 'neutral'}
                      copyable={false}
                    />
                  }
                  onMove={handleMove}
                  onRemove={handleRemove}
                />
              )
            }
            return (
              <AutoGroupOrderItem
                key={routeKey}
                group={routeKey}
                index={index}
                count={props.value.length}
                displayName={option.name}
                label={
                  <span className='inline-flex min-w-0 items-center gap-2'>
                    <span className='min-w-0 truncate'>{option.name}</span>
                    <span className='text-muted-foreground shrink-0 font-mono text-xs'>
                      #{option.id}
                    </span>
                  </span>
                }
                description={
                  option.tag || option.status !== 1 ? (
                    <span className='inline-flex items-center gap-2'>
                      {option.tag ? (
                        <StatusBadge
                          label={option.tag}
                          variant='neutral'
                          copyable={false}
                        />
                      ) : null}
                      {option.status !== 1 ? (
                        <StatusBadge
                          label={t('Disabled')}
                          variant='danger'
                          copyable={false}
                        />
                      ) : null}
                    </span>
                  ) : undefined
                }
                onMove={handleMove}
                onRemove={handleRemove}
              />
            )
          })}
        </Reorder.Group>
      )}
    </div>
  )
}
