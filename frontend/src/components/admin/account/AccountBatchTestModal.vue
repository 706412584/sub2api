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
          <div class="mt-2 grid gap-2 text-xs text-gray-600 dark:text-gray-300 sm:grid-cols-2">
            <div>
              {{ t('admin.accounts.batchTest.progress', { completed: progress.completed, total: progress.total }) }}
            </div>
            <div v-if="currentAccountName">
              {{ t('admin.accounts.batchTest.current', { index: currentAccountIndex, total: progress.total, name: currentAccountName }) }}
            </div>
            <div v-else-if="streamStatusMessage">
              {{ streamStatusMessage }}
            </div>
          </div>
          <div class="mt-2 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
            <div
              class="h-full rounded-full bg-blue-500 transition-all duration-200"
              :style="{ width: `${progressPercent}%` }"
            />
          </div>
        </div>

        <div v-if="failedIds.length > 0" class="space-y-2 rounded-lg border border-red-200 bg-red-50 p-2 text-xs dark:border-red-900/60 dark:bg-red-950/20">
          <div class="flex flex-wrap items-center gap-2">
            <button
              v-for="filter in failureFilters"
              :key="filter.key"
              type="button"
              class="rounded-full border px-2.5 py-1 transition"
              :class="selectedFailureFilter === filter.key
                ? 'border-red-500 bg-red-600 text-white dark:border-red-400 dark:bg-red-500'
                : 'border-red-200 bg-white text-red-700 hover:bg-red-100 dark:border-red-900 dark:bg-dark-800 dark:text-red-300 dark:hover:bg-red-950/40'"
              :disabled="testing || deletingFailed"
              @click="selectedFailureFilter = filter.key"
            >
              {{ filter.label }}
            </button>
          </div>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="text-red-700 dark:text-red-300">
              {{ t('admin.accounts.batchTest.selectedFailedCount', { selected: selectedFailedCount, total: filteredFailedIds.length }) }}
            </div>
            <div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary btn-sm" :disabled="testing || deletingFailed || filteredFailedIds.length === 0" @click="selectFilteredFailed">
                {{ t('admin.accounts.batchTest.selectFilteredFailed') }}
              </button>
              <button class="btn btn-secondary btn-sm" :disabled="testing || deletingFailed" @click="selectAllFailed">
                {{ t('admin.accounts.batchTest.selectAllFailed') }}
              </button>
              <button class="btn btn-secondary btn-sm" :disabled="testing || deletingFailed" @click="invertFailedSelection">
                {{ t('admin.accounts.batchTest.invertFailedSelection') }}
              </button>
              <button class="btn btn-danger btn-sm" :disabled="testing || deletingFailed || selectedFailedCount === 0" @click="deleteSelectedFailedAccounts">
                {{ deletingFailed ? t('admin.accounts.batchTest.deletingFailed') : t('admin.accounts.batchTest.deleteSelectedFailed') }}
              </button>
            </div>
          </div>
        </div>

        <div class="max-h-80 overflow-y-auto rounded-lg border border-gray-200 dark:border-dark-500">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-500">
            <thead class="bg-gray-50 dark:bg-dark-700">
              <tr>
                <th class="w-12 px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.select') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.accountName') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.status') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.latency') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchTest.error') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-600 dark:bg-dark-800">
              <tr v-for="item in visibleResultItems" :key="item.account_id">
                <td class="px-3 py-2">
                  <input
                    v-if="!item.success"
                    type="checkbox"
                    class="h-4 w-4 rounded border-gray-300 text-red-600 focus:ring-red-500 dark:border-dark-500 dark:bg-dark-700"
                    :checked="selectedFailedIdSet.has(item.account_id)"
                    :disabled="testing || deletingFailed"
                    @change="toggleFailedSelection(item.account_id)"
                  />
                  <span v-else class="text-gray-300 dark:text-dark-500">-</span>
                </td>
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
          class="btn btn-danger"
          :disabled="testing || deletingFailed"
          @click="deleteFailedAccounts(failedIds)"
        >
          {{ deletingFailed ? t('admin.accounts.batchTest.deletingFailed') : t('admin.accounts.batchTest.deleteAllFailed') }}
        </button>
        <button
          v-if="failedIds.length > 0"
          class="btn btn-warning"
          :disabled="testing || deletingFailed || !selectedModelId"
          @click="startTest(failedIds)"
        >
          {{ testing ? t('admin.accounts.batchTest.testing') : t('admin.accounts.batchTest.retryFailed') }}
        </button>
        <button
          class="btn btn-primary"
          :disabled="testing || deletingFailed || loadingModels || !selectedModelId || accountIds.length === 0"
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
import type { BatchTestAccountItem, BatchTestAccountsResponse } from '@/api/admin/accounts'

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
const deletingFailed = ref(false)
const selectedFailedIds = ref<number[]>([])
const selectedFailureFilter = ref('all')
const result = ref<BatchTestAccountsResponse | null>(null)
const currentAccountName = ref('')
const currentAccountIndex = ref(0)
const streamStatusMessage = ref('')
const progress = ref({ total: 0, completed: 0, success: 0, failed: 0 })
const testMode = ref<'default' | 'compact'>('default')
let abortController: AbortController | null = null
const prioritizedGeminiModels = ['gemini-3.1-flash-image', 'gemini-2.5-flash-image', 'gemini-3.5-flash', 'gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-3-flash-preview', 'gemini-3-pro-preview', 'gemini-2.0-flash']

const firstAccount = computed(() => props.accounts[0] ?? null)
const isOpenAISelection = computed(() => props.accounts.some(account => account.platform === 'openai'))
const failedItems = computed(() => result.value?.items.filter(item => !item.success) ?? [])
const failedIds = computed(() => failedItems.value.map(item => item.account_id))
const selectedFailedIdSet = computed(() => new Set(selectedFailedIds.value))
const selectedFailedCount = computed(() => selectedFailedIds.value.length)
const failureKey = (item: BatchTestAccountItem) => item.error?.match(/\b(\d{3})\b/)?.[1] ?? (item.status || 'failed')
const failureFilters = computed(() => {
  const counts = new Map<string, number>()
  failedItems.value.forEach(item => counts.set(failureKey(item), (counts.get(failureKey(item)) ?? 0) + 1))
  return [
    { key: 'all', label: t('admin.accounts.batchTest.allFailedTab', { count: failedIds.value.length }) },
    ...Array.from(counts.entries()).sort(([a], [b]) => a.localeCompare(b)).map(([key, count]) => ({
      key,
      label: t('admin.accounts.batchTest.failureTab', { code: key, count })
    }))
  ]
})
const visibleResultItems = computed(() => {
  if (!result.value) return []
  if (selectedFailureFilter.value === 'all') return result.value.items
  return result.value.items.filter(item => !item.success && failureKey(item) === selectedFailureFilter.value)
})
const filteredFailedIds = computed(() => {
  if (selectedFailureFilter.value === 'all') return failedIds.value
  return failedItems.value.filter(item => failureKey(item) === selectedFailureFilter.value).map(item => item.account_id)
})
const progressPercent = computed(() => {
  if (!progress.value.total) return 0
  return Math.min(100, Math.round((progress.value.completed / progress.value.total) * 100))
})
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
      abortStream()
      result.value = null
      selectedFailedIds.value = []
      selectedFailureFilter.value = 'all'
      currentAccountName.value = ''
      currentAccountIndex.value = 0
      streamStatusMessage.value = ''
      progress.value = { total: 0, completed: 0, success: 0, failed: 0 }
      testMode.value = 'default'
      await loadAvailableModels()
    } else {
      abortStream()
      testing.value = false
    }
  }
)

watch(
  () => props.accountIds,
  () => {
    if (props.visible) {
      abortStream()
      result.value = null
      selectedFailedIds.value = []
      selectedFailureFilter.value = 'all'
      currentAccountName.value = ''
      currentAccountIndex.value = 0
      streamStatusMessage.value = ''
      progress.value = { total: 0, completed: 0, success: 0, failed: 0 }
      testing.value = false
    }
  }
)

type BatchTestSSEEvent =
  | { type: 'batch_start'; total: number }
  | { type: 'account_start'; account_id: number; name?: string; index: number; total: number }
  | (BatchTestAccountItem & { type: 'account_result'; index: number; total: number })
  | { type: 'progress'; total: number; completed: number; success: number; failed: number }
  | (BatchTestAccountsResponse & { type: 'batch_complete' })
  | { type: 'error'; error: string }

const resetStreamState = (total: number) => {
  result.value = { total, success: 0, failed: 0, items: [] }
  selectedFailedIds.value = []
  selectedFailureFilter.value = 'all'
  progress.value = { total, completed: 0, success: 0, failed: 0 }
  currentAccountName.value = ''
  currentAccountIndex.value = 0
  modelLoadError.value = ''
}

const abortStream = () => {
  if (abortController) {
    abortController.abort()
    abortController = null
  }
}

const upsertResultItem = (item: BatchTestAccountItem) => {
  if (!result.value) {
    result.value = { total: progress.value.total || 0, success: 0, failed: 0, items: [] }
  }

  const items = [...result.value.items]
  const index = items.findIndex(existing => existing.account_id === item.account_id)
  if (index >= 0) {
    items[index] = item
  } else {
    items.push(item)
  }

  const success = items.filter(existing => existing.success).length
  const failed = items.filter(existing => !existing.success).length
  result.value = {
    total: progress.value.total || result.value.total || items.length,
    success,
    failed,
    items
  }
  progress.value = {
    total: result.value.total,
    completed: Math.max(progress.value.completed, items.length),
    success,
    failed
  }
}

const handleStreamEvent = (event: BatchTestSSEEvent) => {
  streamStatusMessage.value = ''
  switch (event.type) {
    case 'batch_start':
      resetStreamState(event.total)
      break
    case 'account_start':
      currentAccountName.value = event.name || `#${event.account_id}`
      currentAccountIndex.value = event.index
      progress.value.total = event.total
      break
    case 'account_result':
      upsertResultItem({
        account_id: event.account_id,
        name: event.name || `#${event.account_id}`,
        success: event.success,
        status: event.status,
        latency_ms: event.latency_ms,
        error: event.error
      })
      progress.value.total = event.total
      progress.value.completed = Math.max(progress.value.completed, event.index)
      break
    case 'progress':
      progress.value = {
        total: event.total,
        completed: event.completed,
        success: event.success,
        failed: event.failed
      }
      if (result.value) {
        result.value = {
          ...result.value,
          total: event.total,
          success: event.success,
          failed: event.failed
        }
      }
      break
    case 'batch_complete':
      result.value = {
        total: event.total,
        success: event.success,
        failed: event.failed,
        items: event.items
      }
      progress.value = {
        total: event.total,
        completed: event.total,
        success: event.success,
        failed: event.failed
      }
      currentAccountName.value = ''
      currentAccountIndex.value = 0
      emit('completed')
      break
    case 'error':
      modelLoadError.value = event.error
      break
  }
}

const startTest = async (ids: number[]) => {
  if (!selectedModelId.value || ids.length === 0) return
  abortStream()
  resetStreamState(ids.length)
  streamStatusMessage.value = t('admin.accounts.batchTest.waitingForProgress')
  testing.value = true

  try {
    const response = await adminAPI.accounts.batchTestAccounts({
      account_ids: ids,
      model_id: selectedModelId.value,
      mode: isOpenAISelection.value ? testMode.value : undefined
    })
    handleStreamEvent({
      type: 'batch_complete',
      total: response.total,
      success: response.success,
      failed: response.failed,
      items: response.items
    })
  } catch (error) {
    console.error('Failed to batch test accounts:', error)
    modelLoadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    testing.value = false
    streamStatusMessage.value = ''
  }
}

const toggleFailedSelection = (id: number) => {
  if (!failedIds.value.includes(id)) return
  selectedFailedIds.value = selectedFailedIdSet.value.has(id)
    ? selectedFailedIds.value.filter(selectedId => selectedId !== id)
    : [...selectedFailedIds.value, id]
}

const selectFilteredFailed = () => {
  selectedFailedIds.value = [...filteredFailedIds.value]
}

const selectAllFailed = () => {
  selectedFailedIds.value = [...failedIds.value]
}

const invertFailedSelection = () => {
  const selected = selectedFailedIdSet.value
  selectedFailedIds.value = failedIds.value.filter(id => !selected.has(id))
}

const deleteFailedAccounts = async (ids: number[]) => {
  if (ids.length === 0) return
  if (!window.confirm(t('admin.accounts.batchTest.deleteFailedConfirm', { count: ids.length }))) return

  deletingFailed.value = true
  try {
    await Promise.all(ids.map(id => adminAPI.accounts.delete(id)))
    if (result.value) {
      const deleted = new Set(ids)
      const items = result.value.items.filter(item => !deleted.has(item.account_id))
      const success = items.filter(item => item.success).length
      const failed = items.filter(item => !item.success).length
      result.value = {
        total: items.length,
        success,
        failed,
        items
      }
      progress.value = {
        total: items.length,
        completed: items.length,
        success,
        failed
      }
      selectedFailedIds.value = selectedFailedIds.value.filter(id => !deleted.has(id))
    }
    emit('completed')
  } catch (error) {
    console.error('Failed to delete failed accounts:', error)
    modelLoadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    deletingFailed.value = false
  }
}

const deleteSelectedFailedAccounts = () => deleteFailedAccounts(selectedFailedIds.value)

const formatLatency = (latency: number) => {
  if (!latency || latency < 0) return '-'
  return `${latency} ms`
}

const handleClose = () => {
  abortStream()
  emit('update:visible', false)
  emit('close')
}
</script>
