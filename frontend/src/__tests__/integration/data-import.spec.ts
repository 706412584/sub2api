import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'

import { adminAPI } from '@/api/admin'

const showError = vi.fn()
const showSuccess = vi.fn()

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      importData: vi.fn()
    }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

describe('ImportDataModal', () => {
  beforeEach(() => {
    showError.mockReset()
    showSuccess.mockReset()
    vi.mocked(adminAPI.accounts.importData).mockReset()
  })

  it('未选择文件时提示错误', async () => {
    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    await wrapper.find('form').trigger('submit')
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')
  })

  it('无效 JSON 时记录文件级错误', async () => {
    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    const input = wrapper.find('input[type="file"]')
    const file = new File(['invalid json'], 'data.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve('invalid json')
    })
    Object.defineProperty(input.element, 'files', {
      value: [file]
    })

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportCompletedWithErrors')
    expect(wrapper.text()).toContain('[data.json] admin.accounts.dataImportParseFailed')
  })

  it('支持选择多个 JSON 文件并逐个导入后聚合结果', async () => {
    vi.mocked(adminAPI.accounts.importData)
      .mockResolvedValueOnce({
        account_created: 1,
        account_failed: 0,
        proxy_created: 1,
        proxy_reused: 0,
        proxy_failed: 0,
        errors: []
      })
      .mockResolvedValueOnce({
        account_created: 2,
        account_failed: 1,
        proxy_created: 0,
        proxy_reused: 1,
        proxy_failed: 0,
        errors: [{ kind: 'account', name: 'test', message: 'duplicated' }]
      })

    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    const input = wrapper.find('input[type="file"]')
    expect(input.attributes('multiple')).toBeDefined()

    const firstFile = new File(['{"accounts":[]}'], 'first.json', { type: 'application/json' })
    const secondFile = new File(['{"accounts":[]}'], 'second.json', { type: 'application/json' })
    Object.defineProperty(firstFile, 'text', {
      value: () => Promise.resolve('{"accounts":[]}')
    })
    Object.defineProperty(secondFile, 'text', {
      value: () => Promise.resolve('{"accounts":[]}')
    })
    Object.defineProperty(input.element, 'files', {
      value: [firstFile, secondFile]
    })

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(adminAPI.accounts.importData).toHaveBeenCalledTimes(2)
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportCompletedWithErrors')
    expect(wrapper.text()).toContain('admin.accounts.dataImportResultSummary')
    expect(wrapper.text()).toContain('[second.json] duplicated')
  })

  it('批量导入中单个文件失败时继续处理后续文件', async () => {
    vi.mocked(adminAPI.accounts.importData).mockResolvedValueOnce({
      account_created: 1,
      account_failed: 0,
      proxy_created: 0,
      proxy_reused: 0,
      proxy_failed: 0,
      errors: []
    })

    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    const input = wrapper.find('input[type="file"]')
    const invalidFile = new File(['invalid json'], 'invalid.json', { type: 'application/json' })
    const validFile = new File(['{"accounts":[]}'], 'valid.json', { type: 'application/json' })
    Object.defineProperty(invalidFile, 'text', {
      value: () => Promise.resolve('invalid json')
    })
    Object.defineProperty(validFile, 'text', {
      value: () => Promise.resolve('{"accounts":[]}')
    })
    Object.defineProperty(input.element, 'files', {
      value: [invalidFile, validFile]
    })

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(adminAPI.accounts.importData).toHaveBeenCalledTimes(1)
    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportCompletedWithErrors')
    expect(wrapper.text()).toContain('[invalid.json] admin.accounts.dataImportParseFailed')
  })
})
