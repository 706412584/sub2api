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
              {{ t('admin.accounts.batchTest.current', { name: currentAccountName }) }}
            </div>
          </div>
          <div class="mt-2 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
            <div
              class="h-full rounded-full bg-blue-500 transition-all duration-200"
              :style="{ width: `${progressPercent}%` }"
            />
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
const result = ref<BatchTestAccountsResponse | null>(null)
const currentAccountName = ref('')
const progress = ref({ total: 0, completed: 0, success: 0, failed: 0 })
const testMode = ref<'default' | 'compact'>('default')
let abortController: AbortController | null = null
const prioritizedGeminiModels = ['gemini-3.1-flash-image', 'gemini-2.5-flash-image', 'gemini-3.5-flash', 'gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-3-flash-preview', 'gemini-3-pro-preview', 'gemini-2.0-flash']

const firstAccount = computed(() => props.accounts[0] ?? null)
const isOpenAISelection = computed(() => props.accounts.some(account => account.platform === 'openai'))
const failedIds = computed(() => result.value?.items.filter(item => !item.success).map(item => item.account_id) ?? [])
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
      currentAccountName.value = ''
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
      currentAccountName.value = ''
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
  progress.value = { total, completed: 0, success: 0, failed: 0 }
  currentAccountName.value = ''
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
  switch (event.type) {
    case 'batch_start':
      resetStreamState(event.total)
      break
    case 'account_start':
      currentAccountName.value = event.name || `#${event.account_id}`
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
      emit('completed')
      break
    case 'error':
      modelLoadError.value = event.error
      break
  }
}

const processSSELine = (line: string) => {
  if (!line.startsWith('data:')) return

  const jsonStr = line.slice(5).trim()
  if (!jsonStr) return

  try {
    handleStreamEvent(JSON.parse(jsonStr) as BatchTestSSEEvent)
  } catch (error) {
    console.error('Failed to parse batch test SSE event:', error)
  }
}

const startTest = async (ids: number[]) => {
  if (!selectedModelId.value || ids.length === 0) return
  abortStream()
  resetStreamState(ids.length)
  testing.value = true
  abortController = new AbortController()

  try {
    const response = await fetch('/api/v1/admin/accounts/batch-test/stream', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        account_ids: ids,
        model_id: selectedModelId.value,
        mode: isOpenAISelection.value ? testMode.value : undefined
      }),
      signal: abortController.signal
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    const reader = response.body?.getReader()
    if (!reader) {
      throw new Error('No response body')
    }

    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      lines.forEach(processSSELine)
    }

    buffer += decoder.decode()
    buffer.split('\n').forEach(processSSELine)
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      return
    }
    console.error('Failed to batch test accounts:', error)
    modelLoadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    testing.value = false
    abortController = null
  }
}

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
