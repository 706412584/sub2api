import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GrokQuotaProbeCell from '../GrokQuotaProbeCell.vue'
import type { Account } from '@/types'

const { queryQuota } = vi.hoisted(() => ({
  queryQuota: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    grok: { queryQuota }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params?.percent == null ? key : `${key}:${params.percent}`
  })
}))

const account = {
  id: 99,
  platform: 'grok',
  type: 'oauth'
} as Account

describe('GrokQuotaProbeCell', () => {
  beforeEach(() => {
    queryQuota.mockReset()
  })

  it('keeps billing data while exposing a failed Free quota fallback', async () => {
    queryQuota.mockResolvedValue({
      source: 'hybrid_probe',
      billing: { period_type: 'weekly', usage_percent: null },
      snapshot: { status_code: 402, headers_observed: false, updated_at: '2026-07-23T00:00:00Z' },
      status_code: 402,
      headers_observed: false,
      reset_supported: false,
      fetched_at: 1,
      probe_error: 'upstream returned 402 for probe model "grok-4.5"'
    })
    const wrapper = mount(GrokQuotaProbeCell, { props: { account } })

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('upstream returned 402 for probe model "grok-4.5"')
    expect(wrapper.find('.text-amber-600').exists()).toBe(true)
    expect(wrapper.emitted('probed')?.[0]?.[0]).toMatchObject({
      billing: { period_type: 'weekly', usage_percent: null },
      probe_error: 'upstream returned 402 for probe model "grok-4.5"'
    })
  })
})
