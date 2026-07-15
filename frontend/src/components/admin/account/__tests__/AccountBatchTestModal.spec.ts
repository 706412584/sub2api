import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountBatchTestModal from '../AccountBatchTestModal.vue'
import type { Account, ClaudeModel } from '@/types'

const { batchTestAccounts, getAvailableModels } = vi.hoisted(() => ({
  batchTestAccounts: vi.fn(),
  getAvailableModels: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      batchTestAccounts,
      getAvailableModels
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

function makeAccount(id: number, platform: Account['platform']): Account {
  return {
    id,
    name: `account-${id}`,
    platform
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
    batchTestAccounts.mockResolvedValue(emptyResult)
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
})
