<template>
  <BaseDialog
    :show="visible"
    :title="t('admin.accounts.batchTest.title')"
    width="wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-500 dark:bg-dark-700">
        <div class="text-sm font-medium text-gray-900 dark:text-gray-100">
          {{ t('admin.accounts.batchTest.selectedCount', { count: accountIds.length }) }}
        </div>
        <div v-if="accounts.length > 0" class="mt-2 flex flex-wrap gap-2">
          <span
            v-for="account in accounts.slice(0, 8)"
            :key="account.id"
            class="rounded bg-white px-2 py-1 text-xs text-gray-600 dark:bg-dark-600 dark:text-gray-300"
          >
            {{ account.name }}
          </span>
          <span
            v-if="accounts.length > 8"
            class="rounded bg-white px-2 py-1 text-xs text-gray-500 dark:bg-dark-600 dark:text-gray-400"
          >
            {{ t('admin.accounts.batchTest.moreSelected', { count: accounts.length - 8 }) }}
          </span>
        </div>
      </div>

      <div class="space-y-1.5">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.selectTestModel') }}
        </label>
        <Select
          v-model="selectedModelId"
          :options="availableModels"
          :disabled="loadingModels || testing"
          value-key="id"
          label-key="display_name"
          :placeholder="loadingModels ? t('common.loading') + '...' : t('admin.accounts.selectTestModel')"
        />
        <p v-if="modelLoadError" class="text-xs text-red-500">{{ modelLoadError }}</p>
      </div>

      <div v-if="isOpenAISelection" class="space-y-1.5">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.openai.testMode') }}
        </label>
        <Select
          v-model="testMode"
          :options="openAITestModeOptions"
          :disabled="testing"
        />
      </div>

      <div v-if="result" class="space-y-3">
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-500">
          <div class="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {{ t('admin.accounts.batchTest.summary', { success: result.success, failed: result.failed }) }}
          </div>
        </div>

        <div class="max-h-80 overflow-y-auto rounded-lg border border-gray-200 dark:border-dark-500">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-500">
            <thead class="bg-gray-50 dark:bg-dark-700">
              <tr>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.accountName') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.status') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.latency') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.error') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-600 dark:bg-dark-800">
              <tr v-for="item in result.items" :key="item.account_id">
                <td class="px-3 py-2 text-gray-900 dark:text-gray-100">{{ item.name || `#${item.account_id}` }}</td>
                <td class="px-3 py-2">
                  <span :class="item.success ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
                    {{ item.status || (item.success ? t('admin.accounts.batchTest.normal') : t('admin.accounts.batchTest.abnormal')) }}
                  </span>
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-300">{{ formatLatency(item.latency_ms) }}</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-300">{{ item.error || '-' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button class="btn btn-secondary" :disabled="testing" @click="handleClose">
          {{ t('common.close') }}
        </button>
        <button
          v-if="failedIds.length > 0"
          class="btn btn-warning"
          :disabled="testing || !selectedModelId"
          @click="startTest(failedIds)"
        >
          {{ testing ? t('admin.accounts.batchTest.testing') : t('admin.accounts.batchTest.retryFailed') }}
        </button>
        <button
          class="btn btn-primary"
          :disabled="testing || loadingModels || !selectedModelId || accountIds.length === 0"
          @click="startTest(accountIds)"
        >
          {{ testing ? t('admin.accounts.batchTest.testing') : t('admin.accounts.batchTest.start') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { Account, ClaudeModel } from '@/types'
import type { BatchTestAccountsResponse } from '@/api/admin/accounts'

const props = defineProps<{
  visible: boolean
  accountIds: number[]
  accounts: Account[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update:visible', value: boolean): void
  (e: 'completed'): void
}>()

const { t } = useI18n()

const availableModels = ref<ClaudeModel[]>([])
const selectedModelId = ref('')
const loadingModels = ref(false)
const modelLoadError = ref('')
const testing = ref(false)
const result = ref<BatchTestAccountsResponse | null>(null)
const testMode = ref<'default' | 'compact'>('default')
const prioritizedGeminiModels = ['gemini-3.1-flash-image', 'gemini-2.5-flash-image', 'gemini-3.5-flash', 'gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-3-flash-preview', 'gemini-3-pro-preview', 'gemini-2.0-flash']

const firstAccount = computed(() => props.accounts[0] ?? null)
const isOpenAISelection = computed(() => props.accounts.some(account => account.platform === 'openai'))
const failedIds = computed(() => result.value?.items.filter(item => !item.success).map(item => item.account_id) ?? [])
const openAITestModeOptions = computed(() => [
  { value: 'default', label: t('admin.accounts.openai.testModeDefault') },
  { value: 'compact', label: t('admin.accounts.openai.testModeCompact') }
])

const sortTestModels = (models: ClaudeModel[]) => {
  const priorityMap = new Map(prioritizedGeminiModels.map((id, index) => [id, index]))
  return [...models].sort((a, b) => {
    const aPriority = priorityMap.get(a.id) ?? Number.MAX_SAFE_INTEGER
    const bPriority = priorityMap.get(b.id) ?? Number.MAX_SAFE_INTEGER
    if (aPriority !== bPriority) return aPriority - bPriority
    return 0
  })
}

const loadAvailableModels = async () => {
  if (!firstAccount.value) return
  loadingModels.value = true
  modelLoadError.value = ''
  selectedModelId.value = ''
  try {
    const models = await adminAPI.accounts.getAvailableModels(firstAccount.value.id)
    availableModels.value = firstAccount.value.platform === 'gemini' || firstAccount.value.platform === 'antigravity'
      ? sortTestModels(models)
      : models
    if (availableModels.value.length > 0) {
      if (firstAccount.value.platform === 'gemini') {
        selectedModelId.value = availableModels.value[0].id
      } else {
        const sonnetModel = availableModels.value.find(model => model.id.includes('sonnet'))
        selectedModelId.value = sonnetModel?.id || availableModels.value[0].id
      }
    }
  } catch (error) {
    console.error('Failed to load available models:', error)
    availableModels.value = []
    selectedModelId.value = ''
    modelLoadError.value = t('admin.accounts.batchTest.loadModelsFailed')
  } finally {
    loadingModels.value = false
  }
}

watch(
  () => props.visible,
  async (visible) => {
    if (visible) {
      result.value = null
      testMode.value = 'default'
      await loadAvailableModels()
    }
  }
)

watch(
  () => props.accountIds,
  () => {
    if (props.visible) {
      result.value = null
    }
  }
)

const startTest = async (ids: number[]) => {
  if (testing.value || !selectedModelId.value || ids.length === 0) return
  testing.value = true
  try {
    result.value = await adminAPI.accounts.batchTestAccounts({
      account_ids: ids,
      model_id: selectedModelId.value,
      mode: isOpenAISelection.value ? testMode.value : undefined
    })
    emit('completed')
  } catch (error) {
    console.error('Failed to batch test accounts:', error)
    modelLoadError.value = String(error)
  } finally {
    testing.value = false
  }
}

const formatLatency = (latency: number) => {
  if (!latency || latency < 0) return '-'
  return `${latency} ms`
}

const handleClose = () => {
  emit('update:visible', false)
  emit('close')
}
</script>
