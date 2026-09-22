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
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import { SettingsCard } from '../components/settings-card'
import { SettingsSwitchField } from '../components/settings-form-layout'
import { safeNumberFieldProps } from '../utils/numeric-field'
import type { RoutingPolicyFormValues } from './routing-form'

export function DirectRoutingSection() {
  const { t } = useTranslation()
  const form = useFormContext<RoutingPolicyFormValues>()
  return (
    <SettingsCard title={t('Direct channel routing')} className='shadow-none'>
      <FormField
        control={form.control}
        name='EnableDirectChannelRouting'
        render={({ field }) => (
          <SettingsSwitchField
            controlId='EnableDirectChannelRouting'
            checked={field.value}
            onCheckedChange={field.onChange}
            label={t('Enable direct channel routing')}
            description={t(
              'Route presets can select channels by their stable route key. Only administrators can configure them.'
            )}
          />
        )}
      />
      <FormField
        control={form.control}
        name='MaxRoutePresetChannels'
        render={({ field }) => (
          <FormItem className='mt-4'>
            <FormLabel>{t('Maximum channels per route preset')}</FormLabel>
            <FormControl>
              <Input
                type='number'
                min={1}
                max={64}
                step={1}
                {...safeNumberFieldProps(field)}
              />
            </FormControl>
            <FormDescription>
              {t(
                'How many channels one direct route preset may list. Actual attempts are still bounded by retry budgets and the total attempt cap.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </SettingsCard>
  )
}
