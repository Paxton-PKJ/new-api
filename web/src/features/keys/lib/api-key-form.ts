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
import type { TFunction } from 'i18next'
import { nanoid } from 'nanoid'
import { z } from 'zod'

import { validateModelMappingJson } from '@/features/channels/lib/model-mapping-validation'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import { DEFAULT_GROUP } from '../constants'
import type { ApiKey, ApiKeyFormData, TokenProfileConfig } from '../types'

// ============================================================================
// Backend limits
// ============================================================================

/** Backend limits for a token model redirect entry and for the whole mapping. */
const MODEL_REDIRECT_NAME_MAX_LENGTH = 256
const MODEL_REDIRECT_MAX_KIB = 64
const MODEL_REDIRECT_MAX_LENGTH = MODEL_REDIRECT_MAX_KIB * 1024

/** Backend limits for routing profiles and their route presets. */
export const MAX_ROUTING_PROFILES = 16
export const MAX_ROUTE_PRESETS = 8
const PROFILE_NAME_MAX_LENGTH = 64

// ============================================================================
// Form Schema
// ============================================================================

const routePresetFormSchema = z.object({
  /** Client-side identity; profiles and presets can be renamed and reordered. */
  id: z.string(),
  name: z.string(),
  auto_groups: z.array(z.string()),
  cross_group_retry: z.boolean(),
})

export type TokenRoutePresetFormValues = z.infer<typeof routePresetFormSchema>

export const tokenProfileFormSchema = z.object({
  id: z.string(),
  name: z.string(),
  /** Model redirect as the JSON string edited by `ModelMappingEditor`. */
  model_mapping: z.string(),
  model_limits: z.array(z.string()),
  active_route_preset: z.string(),
  route_presets: z.array(routePresetFormSchema),
})

export type TokenProfileFormValues = z.infer<typeof tokenProfileFormSchema>

/**
 * Reports a model redirect chain that loops back on itself. The backend rejects
 * the same shape, and a self-referencing source is an accepted no-op there.
 */
function hasModelRedirectCycle(mapping: Record<string, string>): boolean {
  for (const start of Object.keys(mapping)) {
    const visited = new Set([start])
    let current = start
    while (true) {
      const next = mapping[current]
      if (!next) break
      if (visited.has(next)) {
        if (next === current) break
        return true
      }
      visited.add(next)
      current = next
    }
  }
  return false
}

/**
 * Validates one model redirect JSON string and reports issues at `path`.
 * Token-level redirects and routing-profile redirects share the same backend
 * rules, so both call sites use this check.
 */
function addModelRedirectIssues(
  raw: string | undefined,
  path: (string | number)[],
  ctx: z.RefinementCtx,
  t: TFunction
) {
  const modelMapping = (raw || '').trim()
  if (!modelMapping) return

  // A duplicate source model is emitted as a sentinel that is not valid JSON,
  // so this check covers the editor's "empty" state too.
  const mappingValidation = validateModelMappingJson(modelMapping)
  const entries = mappingValidation.valid
    ? Object.entries(JSON.parse(modelMapping) as Record<string, string>)
    : []
  const hasEmptyEntry =
    !mappingValidation.valid ||
    entries.some(([from, to]) => !from.trim() || !to.trim())

  if (hasEmptyEntry) {
    ctx.addIssue({
      code: 'custom',
      path,
      message: t(
        'Model redirect must be a JSON object with non-empty string keys and values'
      ),
    })
    return
  }

  if (
    entries.some(
      ([from, to]) =>
        from.length > MODEL_REDIRECT_NAME_MAX_LENGTH ||
        to.length > MODEL_REDIRECT_NAME_MAX_LENGTH
    )
  ) {
    ctx.addIssue({
      code: 'custom',
      path,
      message: t('Model redirect entries must be at most {{max}} characters', {
        max: MODEL_REDIRECT_NAME_MAX_LENGTH,
      }),
    })
    return
  }

  if (modelMapping.length > MODEL_REDIRECT_MAX_LENGTH) {
    ctx.addIssue({
      code: 'custom',
      path,
      message: t('Model redirect is too large (max {{max}} KiB)', {
        max: MODEL_REDIRECT_MAX_KIB,
      }),
    })
    return
  }

  if (hasModelRedirectCycle(Object.fromEntries(entries))) {
    ctx.addIssue({
      code: 'custom',
      path,
      message: t('Model redirect must not contain a cycle'),
    })
  }
}

/**
 * Validates every routing profile: names, counts, nested model redirects and
 * model limits, route presets with their Auto groups, and the active pointers.
 */
function addRoutingProfileIssues(
  profiles: TokenProfileFormValues[],
  activeProfile: string,
  group: string | undefined,
  autoGroupLimit: number,
  ctx: z.RefinementCtx,
  t: TFunction
) {
  if (profiles.length > MAX_ROUTING_PROFILES) {
    ctx.addIssue({
      code: 'custom',
      path: ['profiles'],
      message: t('At most {{max}} routing profiles are allowed', {
        max: MAX_ROUTING_PROFILES,
      }),
    })
  }

  const profileNames = new Set<string>()
  profiles.forEach((profile, index) => {
    const name = profile.name.trim()
    const namePath = ['profiles', index, 'name']
    if (!name) {
      ctx.addIssue({
        code: 'custom',
        path: namePath,
        message: t('Please enter a name'),
      })
    } else if (name.length > PROFILE_NAME_MAX_LENGTH) {
      ctx.addIssue({
        code: 'custom',
        path: namePath,
        message: t(
          'Profile and preset names must be at most {{max}} characters',
          { max: PROFILE_NAME_MAX_LENGTH }
        ),
      })
    } else if (profileNames.has(name)) {
      ctx.addIssue({
        code: 'custom',
        path: namePath,
        message: t('Profile names must be unique'),
      })
    }
    profileNames.add(name)

    addModelRedirectIssues(
      profile.model_mapping,
      ['profiles', index, 'model_mapping'],
      ctx,
      t
    )

    if (profile.model_limits.some((limit) => !limit.trim())) {
      ctx.addIssue({
        code: 'custom',
        path: ['profiles', index, 'model_limits'],
        message: t('Model limits must not contain empty entries'),
      })
    }

    if (profile.route_presets.length > MAX_ROUTE_PRESETS) {
      ctx.addIssue({
        code: 'custom',
        path: ['profiles', index, 'route_presets'],
        message: t('At most {{max}} route presets are allowed per profile', {
          max: MAX_ROUTE_PRESETS,
        }),
      })
    }

    const presetNames = new Set<string>()
    profile.route_presets.forEach((preset, presetIndex) => {
      const presetPath = ['profiles', index, 'route_presets', presetIndex]
      const presetName = preset.name.trim()
      if (!presetName) {
        ctx.addIssue({
          code: 'custom',
          path: [...presetPath, 'name'],
          message: t('Please enter a name'),
        })
      } else if (presetName.length > PROFILE_NAME_MAX_LENGTH) {
        ctx.addIssue({
          code: 'custom',
          path: [...presetPath, 'name'],
          message: t(
            'Profile and preset names must be at most {{max}} characters',
            { max: PROFILE_NAME_MAX_LENGTH }
          ),
        })
      } else if (presetNames.has(presetName)) {
        ctx.addIssue({
          code: 'custom',
          path: [...presetPath, 'name'],
          message: t('Route preset names must be unique'),
        })
      }
      presetNames.add(presetName)

      if (preset.auto_groups.length === 0) {
        ctx.addIssue({
          code: 'custom',
          path: [...presetPath, 'auto_groups'],
          message: t('Each route preset needs at least one Auto group'),
        })
      } else if (preset.auto_groups.length > autoGroupLimit) {
        ctx.addIssue({
          code: 'custom',
          path: [...presetPath, 'auto_groups'],
          message: t('Select at most {{max}} Auto groups', {
            max: autoGroupLimit,
          }),
        })
      }

      if (new Set(preset.auto_groups).size !== preset.auto_groups.length) {
        ctx.addIssue({
          code: 'custom',
          path: [...presetPath, 'auto_groups'],
          message: t('Auto groups must not contain duplicates'),
        })
      }
    })

    if (
      profile.active_route_preset.trim() &&
      !presetNames.has(profile.active_route_preset.trim())
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['profiles', index, 'active_route_preset'],
        message: t(
          'The active route preset must reference a preset in this profile'
        ),
      })
    }
  })

  if (activeProfile.trim() && !profileNames.has(activeProfile.trim())) {
    ctx.addIssue({
      code: 'custom',
      path: ['active_profile'],
      message: t(
        'The active routing profile must reference an existing profile'
      ),
    })
  }

  if (
    group !== 'auto' &&
    profiles.some((profile) => profile.route_presets.length > 0)
  ) {
    ctx.addIssue({
      code: 'custom',
      path: ['profiles'],
      message: t('Route presets require the auto group'),
    })
  }
}

export function getApiKeyFormSchema(t: TFunction, maxAutoGroups = 5) {
  const autoGroupLimit =
    Number.isInteger(maxAutoGroups) && maxAutoGroups > 0 ? maxAutoGroups : 5

  return z
    .object({
      name: z.string().min(1, t('Please enter a name')),
      remain_quota_dollars: z.number().optional(),
      expired_time: z.date().optional(),
      unlimited_quota: z.boolean(),
      model_limits: z.array(z.string()),
      model_mapping: z.string().optional(),
      allow_ips: z.string().optional(),
      group: z.string().optional(),
      auto_groups_mode: z.enum(['inherit', 'custom']),
      auto_groups: z.array(z.string()),
      cross_group_retry: z.boolean().optional(),
      tokenCount: z.number().min(1).optional(),
      active_profile: z.string(),
      profiles: z.array(tokenProfileFormSchema),
    })
    .superRefine((data, ctx) => {
      addModelRedirectIssues(data.model_mapping, ['model_mapping'], ctx, t)

      if (data.group === 'auto') {
        if (
          data.auto_groups_mode === 'custom' &&
          data.auto_groups.length === 0
        ) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t(
              'Select at least one Auto group or restore global Auto.'
            ),
          })
        }

        if (data.auto_groups.length > autoGroupLimit) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t('Select at most {{max}} Auto groups', {
              max: autoGroupLimit,
            }),
          })
        }

        if (new Set(data.auto_groups).size !== data.auto_groups.length) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t('Auto groups must not contain duplicates'),
          })
        }
      }

      addRoutingProfileIssues(
        data.profiles,
        data.active_profile,
        data.group,
        autoGroupLimit,
        ctx,
        t
      )

      if (data.unlimited_quota) {
        return
      }

      if (
        data.remain_quota_dollars === undefined ||
        data.remain_quota_dollars < 0
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['remain_quota_dollars'],
          message: t('Quota must be zero or greater'),
        })
      }
    })
}

export type ApiKeyFormValues = z.infer<ReturnType<typeof getApiKeyFormSchema>>

// ============================================================================
// Form Defaults
// ============================================================================

export const API_KEY_FORM_DEFAULT_VALUES: ApiKeyFormValues = {
  name: '',
  remain_quota_dollars: 10,
  expired_time: undefined,
  unlimited_quota: true,
  model_limits: [],
  model_mapping: '',
  allow_ips: '',
  group: DEFAULT_GROUP,
  auto_groups_mode: 'inherit',
  auto_groups: [],
  cross_group_retry: true,
  tokenCount: 1,
  active_profile: '',
  profiles: [],
}

export function getApiKeyFormDefaultValues(
  defaultUseAutoGroup: boolean
): ApiKeyFormValues {
  return {
    ...API_KEY_FORM_DEFAULT_VALUES,
    group: defaultUseAutoGroup ? 'auto' : DEFAULT_GROUP,
    auto_groups_mode: 'inherit',
    auto_groups: [],
    cross_group_retry: defaultUseAutoGroup,
  }
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/** Parses the redirect JSON string; empty, `{}` and unparsable text become none. */
function parseModelRedirect(raw: string): Record<string, string> | undefined {
  const text = (raw || '').trim()
  if (!text) return undefined
  try {
    const parsed: unknown = JSON.parse(text)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return undefined
    }
    const entries = Object.entries(parsed as Record<string, string>).filter(
      ([from, to]) => from.trim() && to.trim()
    )
    if (entries.length === 0) return undefined
    return Object.fromEntries(entries)
  } catch {
    return undefined
  }
}

/** Serializes a stored redirect for the JSON editor; an empty mapping becomes ''. */
function formatModelRedirect(mapping?: Record<string, string>): string {
  if (!mapping) return ''
  const entries = Object.entries(mapping).filter(
    ([from, to]) => from.trim() && to.trim()
  )
  if (entries.length === 0) return ''
  return JSON.stringify(Object.fromEntries(entries), null, 2)
}

/**
 * Builds the stored routing profile document. Empty fields are omitted so the
 * backend keeps treating them as "not configured"; an empty list clears the
 * document with `null`.
 */
function transformProfilesToPayload(
  profiles: TokenProfileFormValues[],
  activeProfile: string
): TokenProfileConfig | null {
  const items = profiles.flatMap((profile) => {
    const name = profile.name.trim()
    if (!name) return []

    const modelMapping = parseModelRedirect(profile.model_mapping)
    const modelLimits = profile.model_limits
      .map((limit) => limit.trim())
      .filter(Boolean)
    const routePresets = profile.route_presets.flatMap((preset) => {
      const presetName = preset.name.trim()
      if (!presetName) return []
      return [
        {
          name: presetName,
          auto_groups: preset.auto_groups,
          cross_group_retry: preset.cross_group_retry,
        },
      ]
    })
    const activeRoutePreset = profile.active_route_preset.trim()

    return [
      {
        name,
        ...(modelMapping ? { model_mapping: modelMapping } : {}),
        ...(modelLimits.length > 0 ? { model_limits: modelLimits } : {}),
        ...(activeRoutePreset
          ? { active_route_preset: activeRoutePreset }
          : {}),
        ...(routePresets.length > 0 ? { route_presets: routePresets } : {}),
      },
    ]
  })

  if (items.length === 0) return null

  const active = activeProfile.trim()
  return active
    ? { active_profile: active, profiles: items }
    : { profiles: items }
}

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: ApiKeyFormValues
): ApiKeyFormData {
  return {
    name: data.name,
    remain_quota: data.unlimited_quota
      ? 0
      : parseQuotaFromDollars(data.remain_quota_dollars || 0),
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : -1,
    unlimited_quota: data.unlimited_quota,
    model_limits_enabled: data.model_limits.length > 0,
    model_limits: data.model_limits.join(','),
    model_mapping: (data.model_mapping || '').trim(),
    allow_ips: data.allow_ips || '',
    group: data.group || '',
    auto_groups:
      data.group === 'auto' && data.auto_groups_mode === 'custom'
        ? data.auto_groups
        : [],
    cross_group_retry: data.group === 'auto' ? !!data.cross_group_retry : false,
    profiles: transformProfilesToPayload(data.profiles, data.active_profile),
  }
}

/**
 * Transform API key data to form defaults
 */
export function transformApiKeyToFormDefaults(
  apiKey: ApiKey,
  availableAutoGroups: string[] = [],
  maxAutoGroups = 5
): ApiKeyFormValues {
  const availableSet = new Set(availableAutoGroups)
  const storedAutoGroups = apiKey.auto_groups ?? []
  const autoGroups = storedAutoGroups
    .filter((group) => availableSet.has(group))
    .slice(0, Math.max(0, maxAutoGroups))
  const autoGroupsMode = storedAutoGroups.length > 0 ? 'custom' : 'inherit'
  const profileConfig: TokenProfileConfig | null = apiKey.profiles ?? null

  return {
    name: apiKey.name,
    remain_quota_dollars: apiKey.unlimited_quota
      ? 0
      : quotaUnitsToDollars(apiKey.remain_quota),
    expired_time:
      apiKey.expired_time > 0
        ? new Date(apiKey.expired_time * 1000)
        : undefined,
    unlimited_quota: apiKey.unlimited_quota,
    model_limits: apiKey.model_limits
      ? apiKey.model_limits.split(',').filter(Boolean)
      : [],
    model_mapping: apiKey.model_mapping || '',
    allow_ips: apiKey.allow_ips || '',
    group: apiKey.group || DEFAULT_GROUP,
    auto_groups_mode: autoGroupsMode,
    auto_groups: autoGroups,
    cross_group_retry: !!apiKey.cross_group_retry,
    tokenCount: 1,
    active_profile: profileConfig?.active_profile ?? '',
    profiles: (profileConfig?.profiles ?? []).map((profile) => ({
      id: nanoid(),
      name: profile.name,
      model_mapping: formatModelRedirect(profile.model_mapping),
      model_limits: profile.model_limits ?? [],
      active_route_preset: profile.active_route_preset ?? '',
      route_presets: (profile.route_presets ?? []).map((preset) => ({
        id: nanoid(),
        name: preset.name,
        auto_groups: preset.auto_groups ?? [],
        cross_group_retry: preset.cross_group_retry,
      })),
    })),
  }
}
