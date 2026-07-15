import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post
  }
}))

import { batchTestAccounts, type BatchTestAccountsRequest } from '@/api/admin/accounts'

describe('admin accounts batch test API', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('allows the server five-minute deadline and forwards the abort signal', async () => {
    const request: BatchTestAccountsRequest = {
      account_ids: [1, 2],
      model_id: ''
    }
    const response = {
      total: 2,
      success: 2,
      failed: 0,
      items: []
    }
    const signal = new AbortController().signal
    post.mockResolvedValue({ data: response })

    const result = await batchTestAccounts(request, signal)

    expect(post).toHaveBeenCalledWith('/admin/accounts/batch-test', request, {
      timeout: 330_000,
      signal
    })
    expect(result).toEqual(response)
  })
})
