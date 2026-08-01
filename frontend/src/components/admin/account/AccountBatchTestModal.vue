<template>
  <BaseDialog
    :show="visible"
    :title="t('admin.accounts.batchTest.title')"
    width="wide"
    :close-on-escape="!testing"
    :show-close-button="!testing"
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
          {{ t('admin.accounts.batchTest.preselectStatus') }}
        </label>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="option in preselectStatusOptions"
            :key="option.value"
            type="button"
            class="rounded-full border px-2.5 py-1 text-xs transition"
            :class="preselectStatus === option.value
              ? 'border-blue-500 bg-blue-600 text-white dark:border-blue-400 dark:bg-blue-500'
              : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
            :disabled="testing || deletingFailed || applyingStatus"
            @click="preselectStatus = option.value"
          >
            {{ option.label }}
            <span class="ml-1 opacity-80">({{ option.count }})</span>
          </button>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.batchTest.preselectStatusHint', { count: statusFilteredAccountIds.length }) }}
        </p>
      </div>

      <div class="space-y-1.5">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.batchTest.proxyOverride') }}
        </label>
        <input
          v-model="proxySearch"
          type="search"
          class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-100"
          :placeholder="t('admin.accounts.batchTest.proxyOverrideSearch')"
          :disabled="testing || deletingFailed || applyingStatus || loadingProxies"
        />
        <div class="max-h-40 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-500">
          <div class="flex flex-wrap gap-2">
            <button
              type="button"
              class="rounded-full border px-2.5 py-1 text-xs transition"
              :class="overrideProxyId === null
                ? 'border-blue-500 bg-blue-600 text-white dark:border-blue-400 dark:bg-blue-500'
                : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
              :disabled="testing || deletingFailed || applyingStatus"
              @click="overrideProxyId = null"
            >
              {{ t('admin.accounts.batchTest.proxyOverrideKeepBound') }}
            </button>
            <button
              type="button"
              class="rounded-full border px-2.5 py-1 text-xs transition"
              :class="overrideProxyId === NO_PROXY_ID
                ? 'border-blue-500 bg-blue-600 text-white dark:border-blue-400 dark:bg-blue-500'
                : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
              :disabled="testing || deletingFailed || applyingStatus"
              @click="overrideProxyId = NO_PROXY_ID"
            >
              {{ t('admin.accounts.batchTest.proxyOverrideDirect') }}
            </button>
            <button
              v-for="proxy in filteredProxyCatalog"
              :key="proxy.id"
              type="button"
              class="rounded-full border px-2.5 py-1 text-xs transition"
              :class="overrideProxyId === proxy.id
                ? 'border-blue-500 bg-blue-600 text-white dark:border-blue-400 dark:bg-blue-500'
                : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
              :disabled="testing || deletingFailed || applyingStatus"
              @click="overrideProxyId = proxy.id"
            >
              {{ proxy.name || t('admin.accounts.batchTest.proxyLabel', { id: proxy.id }) }}
              <span class="ml-1 opacity-80">#{{ proxy.id }}</span>
            </button>
          </div>
          <p v-if="loadingProxies" class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('common.loading') }}...
          </p>
          <p v-else-if="proxyLoadError" class="mt-2 text-xs text-red-500">
            {{ proxyLoadError }}
          </p>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.batchTest.proxyOverrideHint') }}
        </p>
      </div>

      <div class="grid gap-3 sm:grid-cols-2">
        <div class="space-y-1.5">
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.batchTest.intervalSeconds') }}
          </label>
          <input
            v-model.number="intervalSeconds"
            type="number"
            min="0"
            max="60"
            step="1"
            class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-100"
            :disabled="testing || deletingFailed || applyingStatus"
          />
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.batchTest.intervalSecondsHint') }}
          </p>
        </div>
        <div class="space-y-1.5">
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.batchTest.concurrency') }}
          </label>
          <input
            v-model.number="concurrency"
            type="number"
            min="1"
            max="5"
            step="1"
            class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-dark-500 dark:bg-dark-800 dark:text-gray-100"
            :disabled="testing || deletingFailed || applyingStatus"
          />
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ containsGrok
              ? t('admin.accounts.batchTest.concurrencyGrokHint')
              : t('admin.accounts.batchTest.concurrencyHint') }}
          </p>
        </div>
      </div>

      <div v-if="canUseCompactMode" class="rounded-lg border border-gray-200 p-3 dark:border-dark-500">
        <label class="flex items-start gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input
            v-model="useCompactMode"
            type="checkbox"
            class="mt-0.5 h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500 dark:border-dark-500 dark:bg-dark-700"
            :disabled="testing || deletingFailed || applyingStatus"
          />
          <span>
            <span class="font-medium">{{ t('admin.accounts.batchTest.compactMode') }}</span>
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.batchTest.compactModeHint') }}
            </span>
          </span>
        </label>
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
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.batchTest.defaultModelHint') }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.batchTest.readyCountHint', { count: testableAccountIds.length }) }}
        </p>
        <p v-if="containsGrok && testableAccountIds.length > 20" class="text-xs text-amber-600 dark:text-amber-400">
          {{ t('admin.accounts.batchTest.grokRiskHint') }}
        </p>
        <p v-if="modelLoadError" class="text-xs text-red-500">{{ modelLoadError }}</p>
      </div>

      <div v-if="result" class="space-y-3">
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-500">
          <div class="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {{ t('admin.accounts.batchTest.summary', { success: result.success, failed: result.failed }) }}
          </div>
        </div>

        <div
          v-if="!testing && successIds.length > 0"
          class="space-y-2 rounded-lg border border-emerald-200 bg-emerald-50 p-2 text-xs dark:border-emerald-900/60 dark:bg-emerald-950/20"
        >
          <div class="font-medium text-emerald-800 dark:text-emerald-300">
            {{ t('admin.accounts.batchTest.postActionsTitle') }}
          </div>
          <div class="flex flex-wrap gap-2">
            <button
              class="btn btn-success btn-sm"
              :disabled="applyingStatus || deletingFailed"
              @click="applySuccessStatus('enable')"
            >
              {{ applyingStatus ? t('admin.accounts.batchTest.applyingStatus') : t('admin.accounts.batchTest.enableSuccessful') }}
            </button>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="applyingStatus || deletingFailed"
              @click="applySuccessStatus('activate')"
            >
              {{ t('admin.accounts.batchTest.activateSuccessful') }}
            </button>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="applyingStatus || deletingFailed"
              @click="applySuccessStatus('schedulable')"
            >
              {{ t('admin.accounts.batchTest.enableSchedulingSuccessful') }}
            </button>
          </div>
          <p class="text-emerald-700 dark:text-emerald-300">
            {{ t('admin.accounts.batchTest.postActionsHint', { count: successIds.length }) }}
          </p>
        </div>

        <div
          v-if="failedIds.length > 0"
          class="space-y-2 rounded-lg border border-red-200 bg-red-50 p-2 text-xs dark:border-red-900/60 dark:bg-red-950/20"
        >
          <div class="flex flex-wrap items-center gap-2">
            <button
              v-for="filter in failureFilters"
              :key="filter.key"
              type="button"
              class="rounded-full border px-2.5 py-1 transition"
              :class="selectedFailureFilter === filter.key
                ? 'border-red-500 bg-red-600 text-white dark:border-red-400 dark:bg-red-500'
                : 'border-red-200 bg-white text-red-700 hover:bg-red-100 dark:border-red-900 dark:bg-dark-800 dark:text-red-300 dark:hover:bg-red-950/40'"
              :disabled="testing || deletingFailed || applyingStatus"
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
              <button class="btn btn-secondary btn-sm" :disabled="testing || deletingFailed || applyingStatus || filteredFailedIds.length === 0" @click="selectFilteredFailed">
                {{ t('admin.accounts.batchTest.selectFilteredFailed') }}
              </button>
              <button class="btn btn-secondary btn-sm" :disabled="testing || deletingFailed || applyingStatus" @click="selectAllFailed">
                {{ t('admin.accounts.batchTest.selectAllFailed') }}
              </button>
              <button class="btn btn-secondary btn-sm" :disabled="testing || deletingFailed || applyingStatus" @click="invertFailedSelection">
                {{ t('admin.accounts.batchTest.invertFailedSelection') }}
              </button>
              <button class="btn btn-danger btn-sm" :disabled="testing || deletingFailed || applyingStatus || selectedFailedCount === 0" @click="deleteSelectedFailedAccounts">
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
                    :disabled="testing || deletingFailed || applyingStatus"
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
          :disabled="testing || deletingFailed || applyingStatus"
          @click="deleteFailedAccounts(failedIds)"
        >
          {{ deletingFailed ? t('admin.accounts.batchTest.deletingFailed') : t('admin.accounts.batchTest.deleteAllFailed') }}
        </button>
        <button
          v-if="failedIds.length > 0"
          class="btn btn-warning"
          :disabled="testing || deletingFailed || applyingStatus"
          @click="startTest(failedIds)"
        >
          {{ testing ? t('admin.accounts.batchTest.testing') : t('admin.accounts.batchTest.retryFailed') }}
        </button>
        <button
          class="btn btn-primary"
          :disabled="testing || deletingFailed || applyingStatus || loadingModels || testableAccountIds.length === 0"
          @click="startTest(testableAccountIds)"
        >
          {{ testing ? t('admin.accounts.batchTest.testing') : t('admin.accounts.batchTest.start') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { Account, ClaudeModel, Proxy } from '@/types'
import type { BatchTestAccountItem, BatchTestAccountsResponse } from '@/api/admin/accounts'

type PreselectStatus =
  | 'all'
  | 'active'
  | 'inactive'
  | 'error'
  | 'rate_limited'
  | 'temp_unschedulable'
  | 'unschedulable'

type SuccessStatusAction = 'enable' | 'activate' | 'schedulable'

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
const defaultModelOption: ClaudeModel = {
  id: '',
  type: 'model',
  display_name: t('admin.accounts.batchTest.defaultModel'),
  created_at: ''
}
const availableModels = ref<ClaudeModel[]>([defaultModelOption])
const selectedModelId = ref('')
const loadingModels = ref(false)
const modelLoadError = ref('')
const testing = ref(false)
const deletingFailed = ref(false)
const applyingStatus = ref(false)
const selectedFailedIds = ref<number[]>([])
const selectedFailureFilter = ref('all')
const preselectStatus = ref<PreselectStatus>('all')
/** null = keep bound; 0 = force direct; >0 = temporary override from IP management */
const overrideProxyId = ref<number | null>(null)
const proxyCatalog = ref<Proxy[]>([])
const proxySearch = ref('')
const loadingProxies = ref(false)
const proxyLoadError = ref('')
const intervalSeconds = ref(0)
const concurrency = ref(5)
const useCompactMode = ref(false)
const scheduleDefaultsTouched = ref(false)
const result = ref<BatchTestAccountsResponse | null>(null)
let testAbortController: AbortController | null = null
const NO_PROXY_ID = 0
const firstAccount = computed(() => props.accounts[0] ?? null)
const canUseSharedModelOptions = computed(() => {
  if (props.accounts.length !== props.accountIds.length || props.accounts.length === 0) return false
  return new Set(props.accounts.map(account => account.platform)).size === 1
})
const prioritizedGeminiModels = ['gemini-3.1-flash-image', 'gemini-2.5-flash-image', 'gemini-3.5-flash', 'gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-3-flash-preview', 'gemini-3-pro-preview', 'gemini-2.0-flash']

const accountById = computed(() => {
  const map = new Map<number, Account>()
  props.accounts.forEach(account => map.set(account.id, account))
  return map
})

const filteredProxyCatalog = computed(() => {
  const q = proxySearch.value.trim().toLowerCase()
  const list = [...proxyCatalog.value].sort((a, b) => {
    const an = (a.name || '').toLowerCase()
    const bn = (b.name || '').toLowerCase()
    if (an !== bn) return an.localeCompare(bn)
    return a.id - b.id
  })
  if (!q) return list
  return list.filter(proxy => {
    const hay = `${proxy.name || ''} ${proxy.host || ''} #${proxy.id}`.toLowerCase()
    return hay.includes(q)
  })
})

const selectedOverrideProxyLabel = computed(() => {
  if (overrideProxyId.value === null) return t('admin.accounts.batchTest.proxyOverrideKeepBound')
  if (overrideProxyId.value === NO_PROXY_ID) return t('admin.accounts.batchTest.proxyOverrideDirect')
  const found = proxyCatalog.value.find(proxy => proxy.id === overrideProxyId.value)
  if (found?.name) return `${found.name} (#${found.id})`
  return t('admin.accounts.batchTest.proxyLabel', { id: overrideProxyId.value })
})

const statusFilteredAccountIds = computed(() => {
  if (preselectStatus.value === 'all') return [...props.accountIds]
  return props.accountIds.filter(id => classifyAccountStatus(accountById.value.get(id)) === preselectStatus.value)
})

const testableAccountIds = computed(() => [...statusFilteredAccountIds.value])

const containsGrok = computed(() =>
  testableAccountIds.value.some(id => accountById.value.get(id)?.platform === 'grok')
)

const canUseCompactMode = computed(() => {
  if (testableAccountIds.value.length === 0) return false
  return testableAccountIds.value.every(id => {
    const account = accountById.value.get(id)
    return !!account && account.platform === 'openai'
  })
})

const classifyAccountStatus = (account: Account | undefined): PreselectStatus => {
  if (!account) return 'all'
  if (account.status === 'error') return 'error'
  if (account.status === 'inactive') return 'inactive'
  if (account.rate_limit_reset_at && new Date(account.rate_limit_reset_at).getTime() > Date.now()) return 'rate_limited'
  if (account.temp_unschedulable_until && new Date(account.temp_unschedulable_until).getTime() > Date.now()) return 'temp_unschedulable'
  if (!account.schedulable) return 'unschedulable'
  if (account.status === 'active') return 'active'
  return 'all'
}

const statusCounts = computed(() => {
  const counts: Record<PreselectStatus, number> = {
    all: props.accountIds.length,
    active: 0,
    inactive: 0,
    error: 0,
    rate_limited: 0,
    temp_unschedulable: 0,
    unschedulable: 0
  }
  props.accountIds.forEach(id => {
    const status = classifyAccountStatus(accountById.value.get(id))
    if (status !== 'all') counts[status] += 1
  })
  return counts
})

const preselectStatusOptions = computed(() => {
  const options: Array<{ value: PreselectStatus; label: string; count: number }> = [
    { value: 'all', label: t('admin.accounts.allStatus'), count: statusCounts.value.all },
    { value: 'active', label: t('admin.accounts.status.active'), count: statusCounts.value.active },
    { value: 'inactive', label: t('admin.accounts.status.inactive'), count: statusCounts.value.inactive },
    { value: 'error', label: t('admin.accounts.status.error'), count: statusCounts.value.error },
    { value: 'rate_limited', label: t('admin.accounts.status.rateLimited'), count: statusCounts.value.rate_limited },
    { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable'), count: statusCounts.value.temp_unschedulable },
    { value: 'unschedulable', label: t('admin.accounts.status.unschedulable'), count: statusCounts.value.unschedulable }
  ]
  return options.filter(option => option.value === 'all' || option.count > 0)
})

const failedItems = computed(() => result.value?.items.filter(item => !item.success) ?? [])
const failedIds = computed(() => failedItems.value.map(item => item.account_id))
const successIds = computed(() => result.value?.items.filter(item => item.success).map(item => item.account_id) ?? [])
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

const sortTestModels = (models: ClaudeModel[]) => {
  const priorityMap = new Map(prioritizedGeminiModels.map((id, index) => [id, index]))
  return [...models].sort((a, b) => (priorityMap.get(a.id) ?? Number.MAX_SAFE_INTEGER) - (priorityMap.get(b.id) ?? Number.MAX_SAFE_INTEGER))
}

const loadAvailableModels = async () => {
  availableModels.value = [defaultModelOption]
  selectedModelId.value = ''
  modelLoadError.value = ''
  if (!firstAccount.value || !canUseSharedModelOptions.value) return

  loadingModels.value = true
  try {
    const models = await adminAPI.accounts.getAvailableModels(firstAccount.value.id)
    const modelList = Array.isArray(models) ? models : []
    const sortedModels = firstAccount.value.platform === 'gemini' || firstAccount.value.platform === 'antigravity'
      ? sortTestModels(modelList)
      : modelList
    availableModels.value = [defaultModelOption, ...sortedModels]
  } catch (error) {
    console.error('Failed to load available models:', error)
    modelLoadError.value = t('admin.accounts.batchTest.loadModelsFailed')
  } finally {
    loadingModels.value = false
  }
}

const loadProxyCatalog = async () => {
  loadingProxies.value = true
  proxyLoadError.value = ''
  try {
    const proxies = await adminAPI.proxies.getAllWithCount()
    proxyCatalog.value = Array.isArray(proxies) ? proxies : []
    if (
      overrideProxyId.value !== null &&
      overrideProxyId.value !== NO_PROXY_ID &&
      !proxyCatalog.value.some(proxy => proxy.id === overrideProxyId.value)
    ) {
      overrideProxyId.value = null
    }
  } catch (error) {
    console.error('Failed to load proxies for batch test:', error)
    proxyCatalog.value = []
    proxyLoadError.value = t('admin.accounts.batchTest.proxyOverrideLoadFailed')
  } finally {
    loadingProxies.value = false
  }
}

const applyScheduleDefaults = () => {
  if (scheduleDefaultsTouched.value) return
  if (containsGrok.value) {
    intervalSeconds.value = 5
    concurrency.value = 1
  } else {
    intervalSeconds.value = 0
    concurrency.value = 5
  }
}

const resetModalState = () => {
  result.value = null
  selectedFailedIds.value = []
  selectedFailureFilter.value = 'all'
  preselectStatus.value = 'all'
  overrideProxyId.value = null
  proxySearch.value = ''
  scheduleDefaultsTouched.value = false
  useCompactMode.value = false
  modelLoadError.value = ''
  proxyLoadError.value = ''
  applyScheduleDefaults()
}

const clampIntervalSeconds = (value: number) => {
  if (!Number.isFinite(value)) return 0
  return Math.min(60, Math.max(0, Math.floor(value)))
}

const clampConcurrency = (value: number) => {
  if (!Number.isFinite(value)) return 1
  return Math.min(5, Math.max(1, Math.floor(value)))
}

watch(
  () => props.visible,
  async (visible) => {
    if (visible) {
      testAbortController?.abort()
      testAbortController = null
      resetModalState()
      await Promise.all([loadAvailableModels(), loadProxyCatalog()])
    } else {
      testAbortController?.abort()
      testAbortController = null
      testing.value = false
    }
  }
)

watch(
  () => props.accountIds,
  () => {
    if (!props.visible) return
    testAbortController?.abort()
    testAbortController = null
    resetModalState()
    testing.value = false
  }
)

watch(containsGrok, () => {
  if (!props.visible) return
  applyScheduleDefaults()
})

watch(canUseCompactMode, (enabled) => {
  if (!enabled) useCompactMode.value = false
})

watch(intervalSeconds, (value, oldValue) => {
  if (value === oldValue) return
  scheduleDefaultsTouched.value = true
  const next = clampIntervalSeconds(value)
  if (next !== value) intervalSeconds.value = next
})

watch(concurrency, (value, oldValue) => {
  if (value === oldValue) return
  scheduleDefaultsTouched.value = true
  const next = clampConcurrency(value)
  if (next !== value) concurrency.value = next
})

const startTest = async (ids: number[]) => {
  if (testing.value || ids.length === 0) return
  const resolvedIntervalSeconds = clampIntervalSeconds(intervalSeconds.value)
  const resolvedConcurrency = clampConcurrency(concurrency.value)
  intervalSeconds.value = resolvedIntervalSeconds
  concurrency.value = resolvedConcurrency

  const mode = canUseCompactMode.value && useCompactMode.value ? 'compact' : 'default'
  const confirmMessage = t('admin.accounts.batchTest.startConfirm', {
    count: ids.length,
    proxy: selectedOverrideProxyLabel.value,
    interval: resolvedIntervalSeconds,
    concurrency: resolvedConcurrency,
    mode
  })
  if (!window.confirm(confirmMessage)) return

  testing.value = true
  modelLoadError.value = ''
  selectedFailedIds.value = []
  selectedFailureFilter.value = 'all'
  testAbortController = new AbortController()
  try {
    result.value = await adminAPI.accounts.batchTestAccounts({
      account_ids: ids,
      model_id: selectedModelId.value,
      mode,
      override_proxy_id: overrideProxyId.value,
      interval_ms: resolvedIntervalSeconds * 1000,
      concurrency: resolvedConcurrency
    }, testAbortController.signal)
    emit('completed')
  } catch (error) {
    if (!testAbortController.signal.aborted) {
      console.error('Failed to batch test accounts:', error)
      modelLoadError.value = error instanceof Error ? error.message : String(error)
    }
  } finally {
    testAbortController = null
    testing.value = false
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
  const targetIds = [...new Set(ids)]
  if (targetIds.length === 0) return
  if (!window.confirm(t('admin.accounts.batchTest.deleteFailedConfirm', { count: targetIds.length }))) return

  deletingFailed.value = true
  try {
    const settled = await Promise.allSettled(targetIds.map(id => adminAPI.accounts.delete(id)))
    const removableIds = targetIds.filter((_, index) => {
      const outcome = settled[index]
      if (outcome.status === 'fulfilled') return true
      const message = outcome.reason instanceof Error ? outcome.reason.message : String(outcome.reason)
      return message.toLowerCase().includes('account not found')
    })

    if (result.value && removableIds.length > 0) {
      const removed = new Set(removableIds)
      const items = result.value.items.filter(item => !removed.has(item.account_id))
      const success = items.filter(item => item.success).length
      const failed = items.filter(item => !item.success).length
      result.value = {
        total: items.length,
        success,
        failed,
        items
      }
      selectedFailedIds.value = selectedFailedIds.value.filter(id => !removed.has(id))
      if (!failedIds.value.length || !failureFilters.value.some(filter => filter.key === selectedFailureFilter.value)) {
        selectedFailureFilter.value = 'all'
      }
    }

    const failedDelete = settled.find(outcome => outcome.status === 'rejected')
    if (failedDelete && removableIds.length !== targetIds.length) {
      const reason = failedDelete.reason instanceof Error ? failedDelete.reason.message : String(failedDelete.reason)
      modelLoadError.value = reason
    }
    emit('completed')
  } catch (error) {
    console.error('Failed to delete failed accounts:', error)
    modelLoadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    deletingFailed.value = false
  }
}

const deleteSelectedFailedAccounts = () => deleteFailedAccounts([...selectedFailedIds.value])

const applySuccessStatus = async (action: SuccessStatusAction) => {
  const ids = [...successIds.value]
  if (ids.length === 0 || applyingStatus.value) return

  applyingStatus.value = true
  modelLoadError.value = ''
  try {
    if (action === 'enable') {
      await adminAPI.accounts.bulkUpdate(ids, { status: 'active', schedulable: true })
      await adminAPI.accounts.batchClearError(ids)
    } else if (action === 'activate') {
      await adminAPI.accounts.bulkUpdate(ids, { status: 'active' })
      await adminAPI.accounts.batchClearError(ids)
    } else {
      await adminAPI.accounts.bulkUpdate(ids, { schedulable: true })
    }
    emit('completed')
  } catch (error) {
    console.error('Failed to apply success status:', error)
    modelLoadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    applyingStatus.value = false
  }
}

const formatLatency = (latency: number) => latency > 0 ? `${latency} ms` : '-'

const handleClose = () => {
  if (testing.value) return
  testAbortController?.abort()
  testAbortController = null
  emit('update:visible', false)
  emit('close')
}

onUnmounted(() => testAbortController?.abort())
</script>
