import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountBatchTestModal from '../AccountBatchTestModal.vue'
import type { Account, ClaudeModel } from '@/types'

const { batchTestAccounts, getAvailableModels, deleteAccount, bulkUpdate, batchClearError } = vi.hoisted(() => ({
  batchTestAccounts: vi.fn(),
  getAvailableModels: vi.fn(),
  deleteAccount: vi.fn(),
  bulkUpdate: vi.fn(),
  batchClearError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      batchTestAccounts,
      getAvailableModels,
      delete: deleteAccount,
      bulkUpdate,
      batchClearError
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialogStub',
  props: {
    show: Boolean,
    closeOnEscape: {
      type: Boolean,
      default: true
    },
    showCloseButton: {
      type: Boolean,
      default: true
    }
  },
  emits: ['close'],
  template: `
    <div v-if="show">
      <button v-if="showCloseButton" class="dialog-close" @click="$emit('close')">dialog-close</button>
      <slot />
      <slot name="footer" />
    </div>
  `
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: String,
    options: {
      type: Array,
      default: () => []
    },
    disabled: Boolean
  },
  emits: ['update:modelValue'],
  template: '<select class="select-stub" :disabled="disabled" />'
})

function makeAccount(
  id: number,
  platform: Account['platform'],
  overrides: Partial<Account> = {}
): Account {
  return {
    id,
    name: `account-${id}`,
    platform,
    status: 'active',
    schedulable: true,
    rate_limit_reset_at: null,
    temp_unschedulable_until: null,
    ...overrides
  } as Account
}

function mountModal(accountIds: number[], accounts: Account[]) {
  return mount(AccountBatchTestModal, {
    props: {
      visible: false,
      accountIds,
      accounts
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub
      }
    }
  })
}

const emptyResult = {
  total: 0,
  success: 0,
  failed: 0,
  items: []
}

describe('AccountBatchTestModal', () => {
  beforeEach(() => {
    batchTestAccounts.mockReset()
    getAvailableModels.mockReset()
    deleteAccount.mockReset()
    bulkUpdate.mockReset()
    batchClearError.mockReset()
    batchTestAccounts.mockResolvedValue(emptyResult)
    deleteAccount.mockResolvedValue(undefined)
    bulkUpdate.mockResolvedValue({ success: 1, failed: 0, results: [] })
    batchClearError.mockResolvedValue({ success: 1, failed: 0, results: [] })
    vi.stubGlobal('confirm', vi.fn(() => true))
  })

  it('跨页选择不请求当前页账号的模型，并用空 model_id 启动测试', async () => {
    const wrapper = mountModal(
      [1, 2],
      [makeAccount(1, 'openai')]
    )

    await wrapper.setProps({ visible: true })
    await flushPromises()

    expect(getAvailableModels).not.toHaveBeenCalled()
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeUndefined()

    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(batchTestAccounts).toHaveBeenCalledTimes(1)
    expect(batchTestAccounts.mock.calls[0][0]).toEqual({
      account_ids: [1, 2],
      model_id: ''
    })
    expect(batchTestAccounts.mock.calls[0][1]).toBeInstanceOf(AbortSignal)

    wrapper.unmount()
  })

  it('完整混合平台选择不请求共享模型，并用空 model_id 启动测试', async () => {
    const wrapper = mountModal(
      [1, 2],
      [makeAccount(1, 'openai'), makeAccount(2, 'gemini')]
    )

    await wrapper.setProps({ visible: true })
    await flushPromises()

    expect(getAvailableModels).not.toHaveBeenCalled()

    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(batchTestAccounts).toHaveBeenCalledTimes(1)
    expect(batchTestAccounts.mock.calls[0][0]).toEqual({
      account_ids: [1, 2],
      model_id: ''
    })

    wrapper.unmount()
  })

  it('测试运行中隐藏对话框关闭入口并阻止关闭和重复启动', async () => {
    let activeSignal: AbortSignal | undefined
    batchTestAccounts.mockImplementation((_request, signal: AbortSignal) => {
      activeSignal = signal
      return new Promise(() => {})
    })
    const wrapper = mountModal(
      [1, 2],
      [makeAccount(1, 'openai')]
    )

    await wrapper.setProps({ visible: true })
    await flushPromises()
    await wrapper.get('.btn-primary').trigger('click')
    await nextTick()

    const dialog = wrapper.findComponent(BaseDialogStub)
    expect(dialog.props('closeOnEscape')).toBe(false)
    expect(dialog.props('showCloseButton')).toBe(false)
    expect(wrapper.find('.dialog-close').exists()).toBe(false)
    expect(wrapper.get('.btn-secondary').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeDefined()

    await (wrapper.vm as any).startTest()
    dialog.vm.$emit('close')
    await nextTick()

    expect(wrapper.emitted('update:visible')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(batchTestAccounts).toHaveBeenCalledTimes(1)

    wrapper.unmount()
    expect(activeSignal?.aborted).toBe(true)
  })

  it('全部所选账号都在当前页且平台一致时加载共享模型', async () => {
    const models: ClaudeModel[] = [
      {
        id: 'gpt-5.4',
        type: 'model',
        display_name: 'GPT-5.4',
        created_at: ''
      }
    ]
    getAvailableModels.mockResolvedValue(models)
    const wrapper = mountModal(
      [1, 2],
      [makeAccount(1, 'openai'), makeAccount(2, 'openai')]
    )

    await wrapper.setProps({ visible: true })
    await flushPromises()

    expect(getAvailableModels).toHaveBeenCalledTimes(1)
    expect(getAvailableModels).toHaveBeenCalledWith(1)
    expect(wrapper.findComponent(SelectStub).props('options')).toEqual([
      {
        id: '',
        type: 'model',
        display_name: 'admin.accounts.batchTest.defaultModel',
        created_at: ''
      },
      ...models
    ])

    wrapper.unmount()
  })

  it('支持先按状态预选并只测活匹配账号', async () => {
    const wrapper = mountModal(
      [1, 2, 3],
      [
        makeAccount(1, 'openai', { status: 'active' }),
        makeAccount(2, 'openai', { status: 'error' }),
        makeAccount(3, 'openai', { status: 'error' })
      ]
    )

    await wrapper.setProps({ visible: true })
    await flushPromises()

    const errorChip = wrapper.findAll('button').find(btn => btn.text().includes('admin.accounts.status.error'))
    expect(errorChip).toBeTruthy()
    await errorChip!.trigger('click')
    await nextTick()

    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(batchTestAccounts).toHaveBeenCalledTimes(1)
    expect(batchTestAccounts.mock.calls[0][0]).toEqual({
      account_ids: [2, 3],
      model_id: ''
    })

    wrapper.unmount()
  })

  it('测活结束后可重测/删除异常，并对成功账号立即启用', async () => {
    batchTestAccounts.mockResolvedValue({
      total: 3,
      success: 1,
      failed: 2,
      items: [
        { account_id: 1, name: 'ok', success: true, status: 'success', latency_ms: 10, error: '' },
        { account_id: 2, name: 'bad-a', success: false, status: 'failed', latency_ms: 20, error: 'status 400 invalid_grant' },
        { account_id: 3, name: 'bad-b', success: false, status: 'failed', latency_ms: 30, error: 'status 502 upstream' }
      ]
    })

    const wrapper = mountModal(
      [1, 2, 3],
      [makeAccount(1, 'openai'), makeAccount(2, 'openai'), makeAccount(3, 'openai')]
    )

    await wrapper.setProps({ visible: true })
    await flushPromises()
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('admin.accounts.batchTest.retryFailed')
    expect(wrapper.text()).toContain('admin.accounts.batchTest.deleteAllFailed')
    expect(wrapper.text()).toContain('admin.accounts.batchTest.enableSuccessful')

    const enableBtn = wrapper.findAll('button').find(btn => btn.text().includes('admin.accounts.batchTest.enableSuccessful'))
    await enableBtn!.trigger('click')
    await flushPromises()

    expect(bulkUpdate).toHaveBeenCalledWith([1], { status: 'active', schedulable: true })
    expect(batchClearError).toHaveBeenCalledWith([1])

    const retryBtn = wrapper.findAll('button').find(btn => btn.text().includes('admin.accounts.batchTest.retryFailed'))
    await retryBtn!.trigger('click')
    await flushPromises()

    expect(batchTestAccounts).toHaveBeenCalledTimes(2)
    expect(batchTestAccounts.mock.calls[1][0]).toEqual({
      account_ids: [2, 3],
      model_id: ''
    })

    const deleteAllBtn = wrapper.findAll('button').find(btn => btn.text().includes('admin.accounts.batchTest.deleteAllFailed'))
    await deleteAllBtn!.trigger('click')
    await flushPromises()

    expect(deleteAccount).toHaveBeenCalledWith(2)
    expect(deleteAccount).toHaveBeenCalledWith(3)
    expect(wrapper.emitted('completed')?.length).toBeGreaterThanOrEqual(2)

    wrapper.unmount()
  })
})
