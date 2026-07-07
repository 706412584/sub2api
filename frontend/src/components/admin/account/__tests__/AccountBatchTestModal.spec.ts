import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountBatchTestModal from '../AccountBatchTestModal.vue'

const { batchTestAccounts, deleteAccount, getAvailableModels } = vi.hoisted(() => ({
  batchTestAccounts: vi.fn(),
  deleteAccount: vi.fn(),
  getAvailableModels: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      batchTestAccounts,
      delete: deleteAccount,
      getAvailableModels
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.accounts.batchTest.selectedFailedCount') {
          return `${params?.selected}/${params?.total} selected`
        }
        if (key === 'admin.accounts.batchTest.deleteFailedConfirm') {
          return `delete ${params?.count}`
        }
        if (key === 'admin.accounts.batchTest.allFailedTab') {
          return `all ${params?.count}`
        }
        if (key === 'admin.accounts.batchTest.failureTab') {
          return `${params?.code} ${params?.count}`
        }
        return key
      }
    })
  }
})

function mountModal() {
  return mount(AccountBatchTestModal, {
    props: {
      visible: false,
      accountIds: [678, 719],
      accounts: [
        { id: 678, name: 'A678', platform: 'openai', type: 'oauth', status: 'active' },
        { id: 719, name: 'A719', platform: 'openai', type: 'oauth', status: 'active' }
      ]
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Select: { template: '<div class="select-stub"></div>' }
      }
    }
  })
}

const clickStart = async (wrapper: ReturnType<typeof mountModal>) => {
  await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.batchTest.start')!.trigger('click')
  await flushPromises()
}

describe('AccountBatchTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-5.5', display_name: 'GPT 5.5' }])
    batchTestAccounts.mockResolvedValue({
      total: 2,
      success: 0,
      failed: 2,
      items: [
        { account_id: 678, name: 'A678', success: false, status: 'failed', latency_ms: 0, error: 'account not found' },
        { account_id: 719, name: 'A719', success: false, status: 'failed', latency_ms: 0, error: 'account not found' }
      ]
    })
    deleteAccount.mockImplementation((id: number) => {
      if (id === 719) return Promise.reject(new Error('account not found'))
      return Promise.resolve()
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('测活完成后保留结果，不触发父级刷新', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ visible: true })
    await flushPromises()

    await clickStart(wrapper)

    expect(wrapper.text()).toContain('A678')
    expect(wrapper.text()).toContain('A719')
    expect(wrapper.emitted('completed')).toBeUndefined()
  })

  it('删除选中异常后清空结果和选中状态，account not found 也从结果移除', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ visible: true })
    await flushPromises()

    await clickStart(wrapper)

    expect(wrapper.text()).toContain('A678')
    expect(wrapper.text()).toContain('A719')
    expect(wrapper.text()).toContain('0/2 selected')

    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.batchTest.selectAllFailed')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('2/2 selected')

    await wrapper.findAll('button').find(button => button.text() === 'admin.accounts.batchTest.deleteSelectedFailed')!.trigger('click')
    await flushPromises()

    expect(deleteAccount).toHaveBeenCalledWith(678)
    expect(deleteAccount).toHaveBeenCalledWith(719)

    const resultTableText = wrapper.find('tbody').text()
    expect(resultTableText).not.toContain('A678')
    expect(resultTableText).not.toContain('A719')
    expect(wrapper.text()).not.toContain('2/2 selected')
    expect(wrapper.text()).toContain('admin.accounts.batchTest.summary')
  })
})
