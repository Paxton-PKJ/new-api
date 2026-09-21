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
import { describe, expect, test } from 'vitest'

import { apiKeySchema, type ApiKey } from '../../types'
import {
  getApiKeyFormDefaultValues,
  getApiKeyFormSchema,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
  type TokenProfileFormValues,
  type TokenRoutePresetFormValues,
} from '../api-key-form'

const t = ((key: string, options?: Record<string, unknown>) => {
  if (options?.max !== undefined) {
    return key.replace('{{max}}', String(options.max))
  }
  return key
}) as TFunction

const MAX_AUTO_GROUPS = 3

function preset(
  overrides: Partial<TokenRoutePresetFormValues> = {}
): TokenRoutePresetFormValues {
  return {
    id: 'preset-1',
    name: 'normal',
    auto_groups: ['vip'],
    cross_group_retry: true,
    ...overrides,
  }
}

function profile(
  overrides: Partial<TokenProfileFormValues> = {}
): TokenProfileFormValues {
  return {
    id: 'profile-1',
    name: 'dsv4f',
    model_mapping: '',
    model_limits: [],
    active_route_preset: '',
    route_presets: [],
    ...overrides,
  }
}

function parseProfiles(overrides: Record<string, unknown>) {
  return getApiKeyFormSchema(t, MAX_AUTO_GROUPS).safeParse({
    ...getApiKeyFormDefaultValues(true),
    name: 'routed key',
    ...overrides,
  })
}

/** Asserts one schema issue at an exact path so profile-level errors stay located. */
function expectIssue(
  result: ReturnType<typeof parseProfiles>,
  path: (string | number)[],
  message: string
) {
  expect(result.success).toBe(false)
  if (result.success) return
  const issue = result.error.issues.find(
    (candidate) => JSON.stringify(candidate.path) === JSON.stringify(path)
  )
  expect(issue?.message).toBe(message)
}

describe('API key routing profile form schema', () => {
  test('accepts profiles with redirects, limits, route presets and active pointers', () => {
    const result = parseProfiles({
      active_profile: 'dsv4f',
      profiles: [
        profile({
          model_mapping: '{"claude-opus-4-8":"dsv4f"}',
          model_limits: ['dsv4f'],
          active_route_preset: 'normal',
          route_presets: [preset()],
        }),
      ],
    })

    expect(result.success).toBe(true)
  })

  test('accepts a key without profiles so keys keep their base fields', () => {
    expect(parseProfiles({}).success).toBe(true)
  })

  test('rejects duplicate profile names on the offending entry', () => {
    expectIssue(
      parseProfiles({
        profiles: [profile(), profile({ id: 'profile-2' })],
      }),
      ['profiles', 1, 'name'],
      'Profile names must be unique'
    )
  })

  test('rejects an unnamed profile and an over-long profile name', () => {
    expectIssue(
      parseProfiles({ profiles: [profile({ name: '  ' })] }),
      ['profiles', 0, 'name'],
      'Please enter a name'
    )
    expectIssue(
      parseProfiles({ profiles: [profile({ name: 'a'.repeat(65) })] }),
      ['profiles', 0, 'name'],
      'Profile and preset names must be at most 64 characters'
    )
  })

  test('rejects an invalid or cyclic model redirect inside a profile', () => {
    expectIssue(
      parseProfiles({ profiles: [profile({ model_mapping: '{' })] }),
      ['profiles', 0, 'model_mapping'],
      'Model redirect must be a JSON object with non-empty string keys and values'
    )
    expectIssue(
      parseProfiles({
        profiles: [profile({ model_mapping: '{"a":"b","b":"a"}' })],
      }),
      ['profiles', 0, 'model_mapping'],
      'Model redirect must not contain a cycle'
    )
  })

  test('rejects an empty model limit inside a profile', () => {
    expectIssue(
      parseProfiles({ profiles: [profile({ model_limits: ['dsv4f', ' '] })] }),
      ['profiles', 0, 'model_limits'],
      'Model limits must not contain empty entries'
    )
  })

  test('requires at least one Auto group per route preset within the limit', () => {
    expectIssue(
      parseProfiles({
        profiles: [profile({ route_presets: [preset({ auto_groups: [] })] })],
      }),
      ['profiles', 0, 'route_presets', 0, 'auto_groups'],
      'Each route preset needs at least one Auto group'
    )
    expectIssue(
      parseProfiles({
        profiles: [
          profile({
            route_presets: [
              preset({
                auto_groups: ['vip', 'default', 'team', 'extra'],
              }),
            ],
          }),
        ],
      }),
      ['profiles', 0, 'route_presets', 0, 'auto_groups'],
      'Select at most 3 Auto groups'
    )
  })

  test('rejects duplicate route preset names and duplicate Auto groups', () => {
    expectIssue(
      parseProfiles({
        profiles: [
          profile({
            route_presets: [
              preset(),
              preset({ id: 'preset-2', auto_groups: ['default'] }),
            ],
          }),
        ],
      }),
      ['profiles', 0, 'route_presets', 1, 'name'],
      'Route preset names must be unique'
    )
    expectIssue(
      parseProfiles({
        profiles: [
          profile({
            route_presets: [preset({ auto_groups: ['vip', 'vip'] })],
          }),
        ],
      }),
      ['profiles', 0, 'route_presets', 0, 'auto_groups'],
      'Auto groups must not contain duplicates'
    )
  })

  test('rejects active pointers that do not resolve', () => {
    expectIssue(
      parseProfiles({
        active_profile: 'missing',
        profiles: [profile()],
      }),
      ['active_profile'],
      'The active routing profile must reference an existing profile'
    )
    expectIssue(
      parseProfiles({
        profiles: [profile({ active_route_preset: 'missing' })],
      }),
      ['profiles', 0, 'active_route_preset'],
      'The active route preset must reference a preset in this profile'
    )
  })

  test('rejects route presets on a key that is not an Auto group', () => {
    expectIssue(
      parseProfiles({
        group: 'vip',
        profiles: [profile({ route_presets: [preset()] })],
      }),
      ['profiles'],
      'Route presets require the auto group'
    )
  })

  test('enforces the profile and route preset counts', () => {
    expectIssue(
      parseProfiles({
        profiles: Array.from({ length: 17 }, (_, index) =>
          profile({ id: `profile-${index}`, name: `profile-${index}` })
        ),
      }),
      ['profiles'],
      'At most 16 routing profiles are allowed'
    )
    expectIssue(
      parseProfiles({
        profiles: [
          profile({
            route_presets: Array.from({ length: 9 }, (_, index) =>
              preset({ id: `preset-${index}`, name: `preset-${index}` })
            ),
          }),
        ],
      }),
      ['profiles', 0, 'route_presets'],
      'At most 8 route presets are allowed per profile'
    )
  })
})

describe('API key routing profile payload mapping', () => {
  test('sends null when no profile is configured', () => {
    const defaults = getApiKeyFormDefaultValues(false)

    expect(transformFormDataToPayload(defaults).profiles).toBe(null)
    expect(
      transformFormDataToPayload({
        ...defaults,
        active_profile: '',
        profiles: [],
      }).profiles
    ).toBe(null)
  })

  test('drops the client ids and omits empty profile fields', () => {
    const payload = transformFormDataToPayload({
      ...getApiKeyFormDefaultValues(true),
      name: 'routed key',
      active_profile: 'dsv4f',
      profiles: [
        profile({ model_limits: ['dsv4f ', ''] }),
        profile({
          id: 'profile-2',
          name: 'cheap',
          model_mapping: '{ "claude-opus-4-8": "dsv4f" }',
          active_route_preset: 'normal',
          route_presets: [preset({ name: ' normal ' })],
        }),
      ],
    })

    expect(payload.profiles).toEqual({
      active_profile: 'dsv4f',
      profiles: [
        {
          name: 'dsv4f',
          model_limits: ['dsv4f'],
        },
        {
          name: 'cheap',
          model_mapping: { 'claude-opus-4-8': 'dsv4f' },
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

  test('keeps the profiles without an active profile selection', () => {
    const payload = transformFormDataToPayload({
      ...getApiKeyFormDefaultValues(true),
      name: 'routed key',
      profiles: [profile()],
    })

    expect(payload.profiles).toEqual({ profiles: [{ name: 'dsv4f' }] })
  })
})

describe('API key routing profile form defaults', () => {
  const baseApiKey: ApiKey = {
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
    profiles: null,
  }

  test('reads a stored document back into the editor shape', () => {
    const apiKey = apiKeySchema.parse({
      ...baseApiKey,
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
                auto_groups: ['vip', 'default'],
                cross_group_retry: true,
              },
            ],
          },
        ],
      },
    })

    const defaults = transformApiKeyToFormDefaults(apiKey)

    expect(defaults.active_profile).toBe('dsv4f')
    expect(defaults.profiles).toHaveLength(1)
    expect(defaults.profiles[0]?.name).toBe('dsv4f')
    expect(JSON.parse(defaults.profiles[0]?.model_mapping ?? '')).toEqual({
      'claude-opus-4-8': 'dsv4f',
    })
    expect(defaults.profiles[0]?.model_limits).toEqual(['dsv4f'])
    expect(defaults.profiles[0]?.active_route_preset).toBe('normal')
    expect(defaults.profiles[0]?.route_presets).toEqual([
      {
        id: expect.any(String),
        name: 'normal',
        auto_groups: ['vip', 'default'],
        cross_group_retry: true,
      },
    ])
  })

  test('maps a missing document to empty profiles without losing the key fields', () => {
    const legacy: Record<string, unknown> = { ...baseApiKey }
    delete legacy.profiles

    expect(apiKeySchema.parse(legacy).profiles).toBe(null)

    for (const apiKey of [apiKeySchema.parse(legacy), baseApiKey]) {
      const defaults = transformApiKeyToFormDefaults(apiKey)

      expect(defaults.profiles).toEqual([])
      expect(defaults.active_profile).toBe('')
      expect(transformFormDataToPayload(defaults).profiles).toBe(null)
    }
  })
})
