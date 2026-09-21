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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { BadgeCell, TruncatedCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useMediaQuery } from '@/hooks'
import { cn } from '@/lib/utils'

import { GroupRatioBadge, type GroupRatio } from './auto-group-visuals'

type ApiKeyGroupCellProps = {
  crossGroupRetry: boolean
  group: string
  ratio?: GroupRatio
  shouldReduceMotion: boolean
  /** Active routing profile; shown under the group badge when set. */
  profileName?: string
  /** Active route preset of that profile. */
  presetName?: string
}

export function ApiKeyGroupCell(props: ApiKeyGroupCellProps) {
  const { t } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const group = props.group?.trim() || ''
  const profileName = props.profileName?.trim() || ''
  const profileLabel = [profileName, props.presetName?.trim() || '']
    .filter(Boolean)
    .join(' · ')

  let content: ReactNode
  if (group === 'auto') {
    content = (
      <Tooltip>
        <TooltipTrigger
          render={
            <BadgeCell
              data-api-key-group-cell='auto'
              tabIndex={0}
              className={cn(
                'ml-0 gap-3 overflow-visible text-xs',
                isMobile ? 'w-full justify-between' : 'max-w-50'
              )}
            />
          }
        >
          <StatusBadge
            label={t('Cross-group')}
            variant='info'
            copyable={false}
            className='px-0'
          />
          <GroupRatioBadge
            ratio={props.ratio}
            isAuto
            shouldReduceMotion={props.shouldReduceMotion}
          />
        </TooltipTrigger>
        <TooltipContent>
          <span className='text-xs'>
            {t(
              'Automatically selects the best available group with circuit breaker mechanism'
            )}
          </span>
        </TooltipContent>
      </Tooltip>
    )
  } else {
    const ratio =
      group && typeof props.ratio === 'number' ? props.ratio : undefined
    content = (
      <TruncatedCell
        className={isMobile ? 'w-full' : 'max-w-50'}
        tabIndex={0}
        tooltipContent={group || t('Follow user group')}
        tooltipClassName='break-all'
      >
        <GroupBadge
          group={group}
          ratio={ratio}
          ratioLabel={group ? undefined : t('Inherited')}
          className='px-0'
          containerClassName={cn('gap-3', isMobile && 'w-full justify-between')}
        />
      </TruncatedCell>
    )
  }

  if (!profileLabel) return content

  return (
    <div className='flex min-w-0 flex-col gap-1'>
      {content}
      <span
        data-slot='api-key-group-profile'
        title={t('Routing profile')}
        className='text-muted-foreground truncate text-xs'
      >
        {profileLabel}
      </span>
    </div>
  )
}
