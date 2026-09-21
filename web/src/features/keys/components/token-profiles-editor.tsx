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
import { ChevronDown, Plus, Trash2 } from 'lucide-react'
import { nanoid } from 'nanoid'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { FieldError } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { ModelMappingEditor } from '@/features/channels/components/model-mapping-editor'
import { cn } from '@/lib/utils'

import {
  MAX_ROUTE_PRESETS,
  MAX_ROUTING_PROFILES,
  type TokenProfileFormValues,
  type TokenRoutePresetFormValues,
} from '../lib'
import type { ApiKeyGroupOption } from './api-key-group-combobox'
import { AutoGroupOrderEditor } from './auto-group-order-editor'

/** Reads a React Hook Form error message from an arbitrarily shaped error node. */
function readFieldMessage(node: unknown): string | undefined {
  if (typeof node !== 'object' || node === null) return undefined
  const message = (node as { message?: unknown }).message
  return typeof message === 'string' && message ? message : undefined
}

/** Reads one child of a React Hook Form error node, covering array items and `root`. */
function readFieldChild(node: unknown, key: string | number): unknown {
  if (typeof node !== 'object' || node === null) return undefined
  return (node as Record<string | number, unknown>)[key]
}

/** Inline validation message for one editor field. */
function ProfileFieldError(props: { error: unknown; className?: string }) {
  const message = readFieldMessage(props.error)
  if (!message) return null
  return (
    <FieldError
      className={cn('text-xs', props.className)}
      errors={[{ message }]}
    />
  )
}

/** First unused `prefix-N` name, so a new profile or preset starts unnamed-unique. */
function nextProfileName(prefix: string, usedNames: string[]): string {
  const taken = new Set(usedNames.map((name) => name.trim()))
  let index = 1
  while (taken.has(`${prefix}-${index}`)) index += 1
  return `${prefix}-${index}`
}

type TokenProfileCardProps = {
  profile: TokenProfileFormValues
  index: number
  isActive: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
  onChange: (next: TokenProfileFormValues) => void
  onRemove: () => void
  models: string[]
  groupOptions: ApiKeyGroupOption[]
  maxAutoGroups: number
  groupIsAuto: boolean
  disabled?: boolean
  error: unknown
}

function TokenProfileCard(props: TokenProfileCardProps) {
  const { t } = useTranslation()
  const nameId = useId()
  const presetId = useId()
  const profile = props.profile
  const presets = profile.route_presets
  const namedPresets = presets.filter((preset) => preset.name.trim())

  const updatePreset = (id: string, next: TokenRoutePresetFormValues) => {
    const previous = presets.find((preset) => preset.id === id)
    const activePreset = profile.active_route_preset
    const keepsActiveName =
      previous !== undefined &&
      previous.name.trim() === activePreset &&
      previous.name !== next.name
    props.onChange({
      ...profile,
      route_presets: presets.map((preset) =>
        preset.id === id ? next : preset
      ),
      active_route_preset: keepsActiveName ? next.name.trim() : activePreset,
    })
  }

  const removePreset = (id: string) => {
    const removed = presets.find((preset) => preset.id === id)
    props.onChange({
      ...profile,
      route_presets: presets.filter((preset) => preset.id !== id),
      active_route_preset:
        removed && removed.name.trim() === profile.active_route_preset
          ? ''
          : profile.active_route_preset,
    })
  }

  const addPreset = () => {
    props.onChange({
      ...profile,
      route_presets: [
        ...presets,
        {
          id: nanoid(),
          name: nextProfileName(
            'preset',
            presets.map((preset) => preset.name)
          ),
          auto_groups: [],
          cross_group_retry: true,
        },
      ],
    })
  }

  return (
    <Collapsible
      open={props.open}
      onOpenChange={props.onOpenChange}
      data-slot='token-profile-card'
      className='bg-muted/20 rounded-lg border'
    >
      <div className='flex items-center gap-1 p-2'>
        <CollapsibleTrigger
          render={
            <button
              type='button'
              className='hover:bg-muted/40 flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors'
            />
          }
        >
          <span className='min-w-0 flex-1 truncate text-sm font-medium'>
            {profile.name.trim() ||
              t('Profile {{count}}', { count: props.index + 1 })}
          </span>
          {props.isActive && (
            <StatusBadge
              label={t('Active')}
              variant='info'
              copyable={false}
              className='shrink-0'
            />
          )}
          <ChevronDown
            aria-hidden='true'
            className={cn(
              'text-muted-foreground size-4 shrink-0 transition-transform',
              props.open && 'rotate-180'
            )}
          />
        </CollapsibleTrigger>
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          aria-label={t('Remove profile')}
          title={t('Remove profile')}
          onClick={props.onRemove}
          disabled={props.disabled}
        >
          <Trash2 aria-hidden='true' className='size-4' />
        </Button>
      </div>

      <CollapsibleContent>
        <div className='flex flex-col gap-4 px-3 pt-1 pb-3'>
          <div className='grid gap-2'>
            <Label htmlFor={nameId}>{t('Profile name')}</Label>
            <Input
              id={nameId}
              value={profile.name}
              onChange={(event) =>
                props.onChange({ ...profile, name: event.target.value })
              }
              placeholder={t('Enter a name')}
              disabled={props.disabled}
              aria-invalid={
                readFieldMessage(readFieldChild(props.error, 'name')) !==
                undefined
              }
            />
            <ProfileFieldError error={readFieldChild(props.error, 'name')} />
          </div>

          <div className='grid gap-2'>
            <Label>{t('Model Redirect')}</Label>
            <ModelMappingEditor
              value={profile.model_mapping}
              onChange={(value) =>
                props.onChange({ ...profile, model_mapping: value })
              }
              disabled={props.disabled}
              targetModelOptions={props.models}
              labels={{
                from: t('Client request model'),
                to: t('Redirect target model'),
                json: t('Model Redirect'),
              }}
              placeholders={{
                from: 'claude-opus-4-8',
                to: 'target-model',
              }}
              template={{
                'claude-opus-4-8': 'target-model',
              }}
              hints={{
                visual: t(
                  'Requests sent with the model on the left are handled as the model on the right.'
                ),
                json: t(
                  'JSON keys are client request model names; values are redirect target model names.'
                ),
              }}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Rewrites the model name sent by the client before model limits, routing and billing. The target model must be available to this key.'
              )}
            </p>
            <ProfileFieldError
              error={readFieldChild(props.error, 'model_mapping')}
            />
          </div>

          <div className='grid gap-2'>
            <Label>{t('Model Limits')}</Label>
            <MultiSelect
              options={props.models.map((model) => ({
                label: model,
                value: model,
              }))}
              selected={profile.model_limits}
              onChange={(values) =>
                props.onChange({ ...profile, model_limits: values })
              }
              placeholder={t('Select models (empty for allow all)')}
              disabled={props.disabled}
            />
            <ProfileFieldError
              error={readFieldChild(props.error, 'model_limits')}
            />
          </div>

          <div className='grid gap-2'>
            <Label>{t('Route presets')}</Label>
            {props.groupIsAuto ? (
              <div data-slot='route-presets' className='flex flex-col gap-3'>
                {presets.map((preset, presetIndex) => (
                  <div
                    key={preset.id}
                    data-slot='route-preset'
                    className='flex flex-col gap-3 rounded-lg border border-dashed p-3'
                  >
                    <div className='flex items-center gap-2'>
                      <Input
                        aria-label={t('Preset name')}
                        value={preset.name}
                        onChange={(event) =>
                          updatePreset(preset.id, {
                            ...preset,
                            name: event.target.value,
                          })
                        }
                        placeholder={t('Enter a name')}
                        disabled={props.disabled}
                      />
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon-sm'
                        aria-label={t('Remove preset')}
                        title={t('Remove preset')}
                        onClick={() => removePreset(preset.id)}
                        disabled={props.disabled}
                      >
                        <Trash2 aria-hidden='true' className='size-4' />
                      </Button>
                    </div>
                    <AutoGroupOrderEditor
                      value={preset.auto_groups}
                      mode='custom'
                      options={props.groupOptions}
                      globalOptions={[]}
                      maxCount={props.maxAutoGroups}
                      allowInherit={false}
                      onChange={(value) =>
                        updatePreset(preset.id, {
                          ...preset,
                          auto_groups: value.groups.slice(
                            0,
                            props.maxAutoGroups
                          ),
                        })
                      }
                    />
                    <div className='flex items-center justify-between gap-3'>
                      <div className='flex flex-col gap-0.5'>
                        <Label className='text-xs'>
                          {t('Cross-group retry')}
                        </Label>
                        <p className='text-muted-foreground text-xs'>
                          {t(
                            'When enabled, if channels in the current group fail, it will try channels in the next group in order.'
                          )}
                        </p>
                      </div>
                      <Switch
                        aria-label={t('Cross-group retry')}
                        checked={preset.cross_group_retry}
                        onCheckedChange={(checked) =>
                          updatePreset(preset.id, {
                            ...preset,
                            cross_group_retry: checked,
                          })
                        }
                        disabled={props.disabled}
                      />
                    </div>
                    <ProfileFieldError
                      error={readFieldChild(
                        readFieldChild(props.error, 'route_presets'),
                        presetIndex
                      )}
                    />
                  </div>
                ))}

                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={addPreset}
                  disabled={
                    props.disabled || presets.length >= MAX_ROUTE_PRESETS
                  }
                >
                  <Plus aria-hidden='true' className='size-4' />
                  {t('Add preset')}
                </Button>

                {namedPresets.length > 0 && (
                  <div className='grid gap-2'>
                    <Label htmlFor={presetId}>{t('Active preset')}</Label>
                    <Select
                      items={[
                        { value: '', label: t('None') },
                        ...namedPresets.map((preset) => ({
                          value: preset.name.trim(),
                          label: preset.name.trim(),
                        })),
                      ]}
                      value={profile.active_route_preset}
                      onValueChange={(value) =>
                        props.onChange({
                          ...profile,
                          active_route_preset: value ?? '',
                        })
                      }
                    >
                      <SelectTrigger
                        id={presetId}
                        aria-label={t('Active preset')}
                        className='w-full'
                      >
                        <SelectValue placeholder={t('None')} />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value=''>{t('None')}</SelectItem>
                          {namedPresets.map((preset) => (
                            <SelectItem
                              key={preset.id}
                              value={preset.name.trim()}
                            >
                              {preset.name.trim()}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </div>
                )}
              </div>
            ) : (
              <p
                data-slot='route-presets-unavailable'
                className='text-muted-foreground text-xs'
              >
                {t('Route presets are available when the key group is auto.')}
              </p>
            )}
            <ProfileFieldError
              error={readFieldChild(props.error, 'route_presets')}
            />
            <ProfileFieldError
              error={readFieldChild(props.error, 'active_route_preset')}
            />
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

export type TokenProfilesEditorProps = {
  value: TokenProfileFormValues[]
  activeProfile: string
  onChange: (profiles: TokenProfileFormValues[]) => void
  onActiveProfileChange: (name: string) => void
  models: string[]
  groupOptions: ApiKeyGroupOption[]
  maxAutoGroups: number
  groupIsAuto: boolean
  disabled?: boolean
  /** React Hook Form error node of the `profiles` field. */
  errors?: unknown
  /** React Hook Form error node of the `active_profile` field. */
  activeProfileError?: unknown
}

export function TokenProfilesEditor(props: TokenProfilesEditorProps) {
  const { t } = useTranslation()
  const activeProfileId = useId()
  const [collapsedIds, setCollapsedIds] = useState<string[]>([])
  const profiles = props.value
  const namedProfiles = profiles.filter((profile) => profile.name.trim())
  const summaryError = readFieldMessage(props.errors)
    ? props.errors
    : readFieldChild(props.errors, 'root')

  const handleAddProfile = () => {
    props.onChange([
      ...profiles,
      {
        id: nanoid(),
        name: nextProfileName(
          'profile',
          profiles.map((profile) => profile.name)
        ),
        model_mapping: '',
        model_limits: [],
        active_route_preset: '',
        route_presets: [],
      },
    ])
  }

  const handleRemoveProfile = (id: string) => {
    const removed = profiles.find((profile) => profile.id === id)
    props.onChange(profiles.filter((profile) => profile.id !== id))
    if (removed && removed.name.trim() === props.activeProfile.trim()) {
      props.onActiveProfileChange('')
    }
  }

  const handleProfileChange = (id: string, next: TokenProfileFormValues) => {
    const previous = profiles.find((profile) => profile.id === id)
    props.onChange(
      profiles.map((profile) => (profile.id === id ? next : profile))
    )
    if (
      previous &&
      previous.name !== next.name &&
      previous.name.trim() === props.activeProfile.trim()
    ) {
      props.onActiveProfileChange(next.name.trim())
    }
  }

  const handleCollapseChange = (id: string, open: boolean) => {
    setCollapsedIds((ids) =>
      open ? ids.filter((collapsed) => collapsed !== id) : [...ids, id]
    )
  }

  return (
    <div data-slot='token-profiles-editor' className='flex flex-col gap-3'>
      <div className='grid gap-2'>
        <Label htmlFor={activeProfileId}>{t('Active profile')}</Label>
        <Select
          items={[
            { value: '', label: t('None (use key defaults)') },
            ...namedProfiles.map((profile) => ({
              value: profile.name.trim(),
              label: profile.name.trim(),
            })),
          ]}
          value={props.activeProfile}
          onValueChange={(value) => props.onActiveProfileChange(value ?? '')}
        >
          <SelectTrigger
            id={activeProfileId}
            aria-label={t('Active profile')}
            className='w-full'
          >
            <SelectValue placeholder={t('None (use key defaults)')} />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              <SelectItem value=''>{t('None (use key defaults)')}</SelectItem>
              {namedProfiles.map((profile) => (
                <SelectItem key={profile.id} value={profile.name.trim()}>
                  {profile.name.trim()}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <p className='text-muted-foreground text-xs'>
          {t(
            'The active profile overrides the key model redirect, model limits and Auto order.'
          )}
        </p>
        <ProfileFieldError error={props.activeProfileError} />
      </div>

      <div className='flex items-center justify-between gap-3'>
        <div className='flex min-w-0 flex-col gap-0.5'>
          <span className='text-sm font-medium'>{t('Routing Profiles')}</span>
          <span className='text-muted-foreground text-xs'>
            {t(
              'Switch the logical model and channel order of this key without changing the client'
            )}
          </span>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={handleAddProfile}
          disabled={props.disabled || profiles.length >= MAX_ROUTING_PROFILES}
          className='shrink-0'
        >
          <Plus aria-hidden='true' className='size-4' />
          {t('Add profile')}
        </Button>
      </div>

      <ProfileFieldError error={summaryError} className='text-sm' />
      {profiles.map((profile, index) => (
        <TokenProfileCard
          key={profile.id}
          profile={profile}
          index={index}
          isActive={profile.name.trim() === props.activeProfile.trim()}
          open={!collapsedIds.includes(profile.id)}
          onOpenChange={(open) => handleCollapseChange(profile.id, open)}
          onChange={(next) => handleProfileChange(profile.id, next)}
          onRemove={() => handleRemoveProfile(profile.id)}
          models={props.models}
          groupOptions={props.groupOptions}
          maxAutoGroups={props.maxAutoGroups}
          groupIsAuto={props.groupIsAuto}
          disabled={props.disabled}
          error={readFieldChild(props.errors, index)}
        />
      ))}
    </div>
  )
}
