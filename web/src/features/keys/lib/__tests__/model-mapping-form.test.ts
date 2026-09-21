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
} from '../api-key-form'

const t = ((key: string, options?: Record<string, unknown>) => {
  if (options?.max !== undefined) {
    return key.replace('{{max}}', String(options.max))
  }
  return key
}) as TFunction

const INVALID_MESSAGE =
  'Model redirect must be a JSON object with non-empty string keys and values'

const baseApiKey: ApiKey = {
  id: 7,
  name: 'test',
  key: 'sk-test',
  status: 1,
  remain_quota: 0,
  used_quota: 0,
  unlimited_quota: true,
  expired_time: -1,
  created_time: 1,
  accessed_time: 0,
  group: 'default',
  auto_groups: null,
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  model_mapping: '',
  allow_ips: '',
  profiles: null,
}

function parseForm(modelMapping: unknown) {
  return getApiKeyFormSchema(t).safeParse({
    ...getApiKeyFormDefaultValues(false),
    name: 'mapped token',
    model_mapping: modelMapping,
  })
}

describe('API key model redirect form schema', () => {
  test('accepts an empty or blank redirect so a stored mapping can be cleared', () => {
    expect(parseForm('').success).toBe(true)
    expect(parseForm('   ').success).toBe(true)
    expect(parseForm(undefined).success).toBe(true)
    expect(parseForm('{"claude-opus-4-8":"dsv4f"}').success).toBe(true)
  })

  test.each([
    ['a JSON array', '[1]'],
    ['a non-string value', '{"a":1}'],
    ['an empty source model', '{"":"x"}'],
    ['a blank target model', '{"a":" "}'],
    ['the duplicate source sentinel', '{ "duplicate_source_models": '],
    ['truncated JSON', '{'],
  ])('rejects %s on the model_mapping field', (_case, mapping) => {
    const result = parseForm(mapping)

    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues[0]?.path).toEqual(['model_mapping'])
    expect(result.error.issues[0]?.message).toBe(INVALID_MESSAGE)
  })

  test('rejects a model name longer than the 256 character limit', () => {
    const result = parseForm(JSON.stringify({ ['a'.repeat(257)]: 'dsv4f' }))

    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues[0]?.path).toEqual(['model_mapping'])
    expect(result.error.issues[0]?.message).toBe(
      'Model redirect entries must be at most 256 characters'
    )
  })

  test('accepts a model name of exactly 256 characters', () => {
    expect(
      parseForm(JSON.stringify({ ['a'.repeat(256)]: 'dsv4f' })).success
    ).toBe(true)
  })

  test('rejects a mapping larger than 64 KiB when every entry fits the name limit', () => {
    const oversized: Record<string, string> = {}
    for (let index = 0; index < 300; index++) {
      oversized[`key-${index}`.padEnd(250, 'a')] = `value-${index}`.padEnd(
        250,
        'b'
      )
    }
    const mapping = JSON.stringify(oversized)
    expect(mapping.length).toBeGreaterThan(64 * 1024)

    const result = parseForm(mapping)

    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues[0]?.path).toEqual(['model_mapping'])
    expect(result.error.issues[0]?.message).toBe(
      'Model redirect is too large (max 64 KiB)'
    )
  })

  test('still validates the redirect when the quota is unlimited', () => {
    const result = getApiKeyFormSchema(t).safeParse({
      ...getApiKeyFormDefaultValues(false),
      name: 'unlimited token',
      unlimited_quota: true,
      model_mapping: '{"a":1}',
    })

    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues[0]?.path).toEqual(['model_mapping'])
    expect(result.error.issues[0]?.message).toBe(INVALID_MESSAGE)
  })
})

describe('API key model redirect form mapping', () => {
  test('sends the trimmed redirect and clears it with an empty string', () => {
    const defaults = getApiKeyFormDefaultValues(false)

    expect(
      transformFormDataToPayload({
        ...defaults,
        model_mapping: '  {"claude-opus-4-8":"dsv4f"}  ',
      }).model_mapping
    ).toBe('{"claude-opus-4-8":"dsv4f"}')
    expect(
      transformFormDataToPayload({ ...defaults, model_mapping: '   ' })
        .model_mapping
    ).toBe('')
  })

  test('loads a stored redirect and maps null or missing responses to an empty value', () => {
    expect(
      transformApiKeyToFormDefaults({
        ...baseApiKey,
        model_mapping: '{"claude-opus-4-8":"dsv4f"}',
      }).model_mapping
    ).toBe('{"claude-opus-4-8":"dsv4f"}')

    const withoutMapping: Record<string, unknown> = { ...baseApiKey }
    delete withoutMapping.model_mapping
    expect(apiKeySchema.parse(withoutMapping).model_mapping).toBe('')
    expect(
      transformApiKeyToFormDefaults(apiKeySchema.parse(withoutMapping))
        .model_mapping
    ).toBe('')
    expect(
      transformApiKeyToFormDefaults(
        apiKeySchema.parse({ ...baseApiKey, model_mapping: null })
      ).model_mapping
    ).toBe('')
  })
})
