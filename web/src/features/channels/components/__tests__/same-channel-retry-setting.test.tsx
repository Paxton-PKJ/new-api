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
import { describe, expect, test } from 'vitest'

import { getChannelConfigurationState } from '../../lib/channel-configuration'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'

function makeChannel(setting?: string) {
  return channelSchema.parse({
    id: 1,
    name: 'Test channel',
    key: '',
    type: 1,
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    setting,
  })
}

// JSON produced by an earlier drawer save for a channel that never touched the
// same-channel retry budget.
const savedSetting = JSON.stringify({
  force_format: false,
  thinking_to_content: false,
  proxy: '',
  pass_through_body_enabled: false,
  responses_websocket_enabled: false,
  system_prompt: '',
  system_prompt_override: false,
})

function valuesWith(sameChannelRetries: number | null): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Test channel',
    models: 'gpt-4o',
    same_channel_retry_times: sameChannelRetries,
  }
}

describe('Same-channel retry channel setting', () => {
  test.each([
    [undefined, null],
    ['{"same_channel_retry_times":0}', 0],
    ['{"same_channel_retry_times":2}', 2],
    ['{"same_channel_retry_times":10}', 10],
    ['{"same_channel_retry_times":"x"}', null],
    ['{"same_channel_retry_times":11}', null],
    ['{"same_channel_retry_times":-1}', null],
    ['{"same_channel_retry_times":1.5}', null],
  ])('loads %s as %s', (setting, expected) => {
    const values = transformChannelToFormDefaults(makeChannel(setting))
    expect(values.same_channel_retry_times).toBe(expected)
  })

  test.each([
    [null, false],
    [0, true],
    [3, true],
  ])('saving %s stores the key: %s', (retries, stored) => {
    const settings = JSON.parse(buildSettingJSON(valuesWith(retries)))
    expect('same_channel_retry_times' in settings).toBe(stored)
    if (stored) {
      expect(settings.same_channel_retry_times).toBe(retries)
    }
  })

  test('an inherited budget round-trips to the saved JSON without adding the key', () => {
    const values = transformChannelToFormDefaults(makeChannel(savedSetting))
    const settings = JSON.parse(buildSettingJSON(values))
    expect(settings).toEqual(JSON.parse(savedSetting))
    expect(settings).not.toHaveProperty('same_channel_retry_times')
  })

  test('a channel that overrides the default keeps its JSON equivalent', () => {
    const original = JSON.stringify({
      ...JSON.parse(savedSetting),
      same_channel_retry_times: 3,
    })
    const values = transformChannelToFormDefaults(makeChannel(original))
    expect(values.same_channel_retry_times).toBe(3)
    expect(JSON.parse(buildSettingJSON(values))).toEqual(JSON.parse(original))
  })

  test.each([11, -1])(
    'validation rejects %s and points at the field',
    (retries) => {
      const result = channelFormSchema.safeParse(valuesWith(retries))
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues.map((issue) => issue.path[0])).toContain(
          'same_channel_retry_times'
        )
      }
    }
  )

  test.each([null, 0, 10])('validation accepts %s', (retries) => {
    expect(channelFormSchema.safeParse(valuesWith(retries)).success).toBe(true)
  })

  test('the extra settings block counts as configured only for an explicit override', () => {
    const errors = {}
    expect(
      getChannelConfigurationState(valuesWith(null), errors, true).blocks
        .extraSettings
    ).toBe('idle')
    expect(
      getChannelConfigurationState(valuesWith(0), errors, true).blocks
        .extraSettings
    ).toBe('configured')
  })
})
