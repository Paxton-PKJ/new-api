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
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { handleServerError } from '@/lib/handle-server-error'

import { switchApiKeyProfile } from '../../api'
import { ERROR_MESSAGES } from '../../constants'
import type { ApiKey, TokenRoutePreset } from '../../types'
import { useApiKeys } from '../api-keys-provider'

type ProfileSwitchDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  apiKey: ApiKey | null
}

export function ProfileSwitchDialog(props: ProfileSwitchDialogProps) {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const profileId = useId()
  const presetId = useId()
  const [activeProfile, setActiveProfile] = useState('')
  const [activePreset, setActivePreset] = useState('')
  const [isSaving, setIsSaving] = useState(false)

  const profileConfig = props.apiKey?.profiles ?? null
  const profiles = profileConfig?.profiles ?? []
  const presets = profiles.find(
    (profile) => profile.name === activeProfile
  )?.route_presets
  const hasPresets = (presets?.length ?? 0) > 0

  // A preset is direct when it stores route keys; the suffix tells the two
  // selection styles apart without opening the editor.
  const presetLabel = (preset: TokenRoutePreset) =>
    preset.route_keys.length > 0
      ? `${preset.name} (${t('Channels')} · ${preset.route_keys.length})`
      : `${preset.name} (${t('Groups')} · ${preset.auto_groups.length})`

  useEffect(() => {
    if (!props.open) return
    const storedProfile = profileConfig?.active_profile ?? ''
    setActiveProfile(storedProfile)
    setActivePreset(
      profileConfig?.profiles.find((profile) => profile.name === storedProfile)
        ?.active_route_preset ?? ''
    )
  }, [props.open, profileConfig])

  const handleProfileChange = (name: string) => {
    setActiveProfile(name)
    setActivePreset(
      profiles.find((profile) => profile.name === name)?.active_route_preset ??
        ''
    )
  }

  const handleSubmit = async () => {
    const apiKey = props.apiKey
    if (!apiKey) return

    setIsSaving(true)
    try {
      // Clearing the profile must not carry a preset: the backend rejects a
      // preset switch while no profile is active.
      const result = await switchApiKeyProfile(
        apiKey.id,
        activeProfile
          ? {
              active_profile: activeProfile,
              active_route_preset: activePreset,
            }
          : { active_profile: '' }
      )
      if (result.success) {
        toast.success(t('Routing profile updated'))
        props.onOpenChange(false)
        triggerRefresh()
      } else {
        handleServerError(result, t(ERROR_MESSAGES.UPDATE_FAILED))
      }
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Switch Routing Profile')}
      description={t(
        'Takes effect on the next request. Edit the key to change profile contents.'
      )}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleSubmit} disabled={isSaving || !props.apiKey}>
            {isSaving ? t('Saving...') : t('Save changes')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label htmlFor={profileId}>{t('Active profile')}</Label>
          <Select
            items={[
              { value: '', label: t('None (use key defaults)') },
              ...profiles.map((profile) => ({
                value: profile.name,
                label: profile.name,
              })),
            ]}
            value={activeProfile}
            onValueChange={(value) => handleProfileChange(value ?? '')}
          >
            <SelectTrigger
              id={profileId}
              aria-label={t('Active profile')}
              className='w-full'
            >
              <SelectValue placeholder={t('None (use key defaults)')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value=''>{t('None (use key defaults)')}</SelectItem>
                {profiles.map((profile) => (
                  <SelectItem key={profile.name} value={profile.name}>
                    {profile.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <div className='space-y-2'>
          <Label htmlFor={presetId}>{t('Active route preset')}</Label>
          <Select
            items={[
              { value: '', label: t('None') },
              ...(presets ?? []).map((preset) => ({
                value: preset.name,
                label: presetLabel(preset),
              })),
            ]}
            value={activePreset}
            onValueChange={(value) => setActivePreset(value ?? '')}
            disabled={!activeProfile || !hasPresets}
          >
            <SelectTrigger
              id={presetId}
              aria-label={t('Active route preset')}
              className='w-full'
              disabled={!activeProfile || !hasPresets}
            >
              <SelectValue placeholder={t('None')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value=''>{t('None')}</SelectItem>
                {(presets ?? []).map((preset) => (
                  <SelectItem key={preset.name} value={preset.name}>
                    {presetLabel(preset)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      </div>
    </Dialog>
  )
}
