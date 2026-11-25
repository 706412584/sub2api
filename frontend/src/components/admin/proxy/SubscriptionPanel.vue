<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4">
    <!-- Engine status -->
    <div
      class="flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3 text-sm"
      :class="engineBannerClass"
    >
      <Icon
        :name="engineStatus?.binary_found ? (engineStatus.running_count > 0 ? 'check' : 'infoCircle') : 'exclamationTriangle'"
        size="md"
      />
      <div class="min-w-0 flex-1">
        <div class="font-medium">
          {{ engineBannerTitle }}
        </div>
        <div class="mt-0.5 text-xs opacity-80">
          <span v-if="engineStatus?.binary_found">
            {{ t('admin.proxies.subscriptions.engineBinary', { path: engineStatus.binary_path || '-' }) }}
            ·
            {{ t('admin.proxies.subscriptions.engineRunningCount', { count: engineStatus.running_count }) }}
          </span>
          <span v-else>
            {{ t('admin.proxies.subscriptions.engineMissingHint') }}
          </span>
        </div>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        :disabled="engineLoading"
        @click="loadEngineStatus"
      >
        <Icon name="refresh" size="sm" :class="engineLoading ? 'animate-spin' : ''" />
      </button>
    </div>

    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-3">
      <div class="relative w-full sm:w-64">
        <Icon
          name="search"
          size="md"
          class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
        />
        <input
          v-model="searchQuery"
          type="text"
          class="input pl-10"
          :placeholder="t('admin.proxies.subscriptions.searchPlaceholder')"
          @input="handleSearch"
        />
      </div>
      <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="reload">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button type="button" class="btn btn-primary" @click="openCreate">
          <Icon name="plus" size="md" class="mr-2" />
          {{ t('admin.proxies.subscriptions.create') }}
        </button>
      </div>
    </div>

    <!-- List -->
    <div class="min-h-0 flex-1 overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
        <thead class="bg-gray-50 dark:bg-dark-800">
          <tr>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.name') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.prefix') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.ports') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.source') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.lastSync') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.enabled') }}
            </th>
            <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.subscriptions.columns.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-900">
          <tr v-if="loading && items.length === 0">
            <td colspan="7" class="px-4 py-10 text-center text-gray-400">
              {{ t('common.loading') }}
            </td>
          </tr>
          <tr v-else-if="items.length === 0">
            <td colspan="7" class="px-4 py-10 text-center text-gray-400">
              {{ t('admin.proxies.subscriptions.empty') }}
            </td>
          </tr>
          <tr v-for="row in items" :key="row.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
            <td class="px-4 py-3">
              <div class="font-medium text-gray-900 dark:text-white">{{ row.name }}</div>
              <div class="mt-0.5 text-xs text-gray-400">
                {{ t('admin.proxies.subscriptions.desiredCount', { count: row.desired_count }) }}
                <span v-if="(row.node_identity_allowlist || []).length > 0">
                  · {{ t('admin.proxies.subscriptions.form.whitelistCount', { count: row.node_identity_allowlist.length }) }}
                </span>
                · {{ row.protocol }}
              </div>
            </td>
            <td class="px-4 py-3">
              <code class="code text-xs">{{ row.name_prefix }}</code>
            </td>
            <td class="px-4 py-3 text-gray-700 dark:text-gray-300">
              {{ row.base_port }}–{{ row.base_port + Math.max(row.max_ports, 1) - 1 }}
              <span class="text-xs text-gray-400">({{ row.max_ports }})</span>
            </td>
            <td class="px-4 py-3">
              <div class="text-gray-700 dark:text-gray-300">
                {{ row.source_type === 'inline' ? t('admin.proxies.subscriptions.sourceInline') : t('admin.proxies.subscriptions.sourceUrl') }}
              </div>
              <div class="mt-0.5 max-w-[220px] truncate text-xs text-gray-400" :title="sourceHint(row)">
                {{ sourceHint(row) }}
              </div>
            </td>
            <td class="px-4 py-3">
              <div class="flex items-center gap-1.5">
                <span :class="['badge', syncStatusBadge(row.last_sync_status)]">
                  {{ syncStatusLabel(row.last_sync_status) }}
                </span>
                <span class="text-xs text-gray-400">
                  {{ row.last_sync_at ? formatDateTime(row.last_sync_at) : '-' }}
                </span>
              </div>
              <div
                v-if="row.last_sync_error"
                class="mt-1 max-w-[260px] truncate text-xs text-red-500"
                :title="row.last_sync_error"
              >
                {{ row.last_sync_error }}
              </div>
            </td>
            <td class="px-4 py-3">
              <button
                type="button"
                class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
                :class="row.enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'"
                :disabled="togglingIds.has(row.id)"
                @click="toggleEnabled(row)"
              >
                <span
                  class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out"
                  :class="row.enabled ? 'translate-x-5' : 'translate-x-0'"
                />
              </button>
            </td>
            <td class="px-4 py-3">
              <div class="flex items-center justify-end gap-1">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="syncingIds.has(row.id)"
                  :title="t('admin.proxies.subscriptions.syncNow')"
                  @click="handleSync(row)"
                >
                  <Icon name="play" size="sm" :class="syncingIds.has(row.id) ? 'animate-pulse' : ''" />
                  <span class="ml-1 hidden sm:inline">{{ t('admin.proxies.subscriptions.syncNow') }}</span>
                </button>
                <button
                  type="button"
                  class="rounded-lg p-1.5 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                  :title="t('common.edit')"
                  @click="openEdit(row)"
                >
                  <Icon name="edit" size="sm" />
                </button>
                <button
                  type="button"
                  class="rounded-lg p-1.5 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                  :title="t('common.delete')"
                  @click="confirmDelete(row)"
                >
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Pagination
      v-if="pagination.total > 0"
      :page="pagination.page"
      :total="pagination.total"
      :page-size="pagination.page_size"
      @update:page="onPageChange"
      @update:pageSize="onPageSizeChange"
    />

    <!-- Create / Edit dialog -->
    <BaseDialog
      :show="showForm"
      :title="editing ? t('admin.proxies.subscriptions.edit') : t('admin.proxies.subscriptions.create')"
      width="wide"
      @close="closeForm"
    >
      <form class="space-y-4" @submit.prevent="submitForm">
        <div class="grid gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2">
            <label class="label">{{ t('admin.proxies.subscriptions.form.name') }}</label>
            <input v-model="form.name" type="text" class="input" maxlength="100" required />
          </div>

          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.sourceType') }}</label>
            <Select
              v-model="form.source_type"
              :options="sourceTypeOptions"
            />
          </div>
          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.protocol') }}</label>
            <Select
              v-model="form.protocol"
              :options="protocolOptions"
            />
          </div>

          <div v-if="form.source_type === 'url'" class="sm:col-span-2">
            <label class="label">{{ t('admin.proxies.subscriptions.form.subscriptionUrl') }}</label>
            <input
              v-model="form.subscription_url"
              type="url"
              class="input"
              maxlength="2000"
              :placeholder="editing ? t('admin.proxies.subscriptions.form.urlLeaveEmpty') : 'https://...'"
            />
            <p v-if="editing" class="mt-1 text-xs text-gray-400">
              {{ t('admin.proxies.subscriptions.form.currentUrlMasked', { url: editing.subscription_url_masked || '-' }) }}
            </p>
          </div>

          <div v-else class="sm:col-span-2">
            <label class="label">{{ t('admin.proxies.subscriptions.form.inlineBody') }}</label>
            <textarea
              v-model="form.inline_body"
              class="input min-h-[140px] font-mono text-xs"
              :placeholder="editing && editing.has_inline_body
                ? t('admin.proxies.subscriptions.form.inlineLeaveEmpty')
                : t('admin.proxies.subscriptions.form.inlinePlaceholder')"
            />
          </div>

          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.namePrefix') }}</label>
            <input
              v-model="form.name_prefix"
              type="text"
              class="input font-mono text-sm"
              maxlength="40"
              placeholder="sidecar-a-"
            />
            <p class="mt-1 text-xs text-gray-400">
              {{ t('admin.proxies.subscriptions.form.namePrefixHint') }}
            </p>
          </div>
          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.bindAddress') }}</label>
            <input v-model="form.bind_address" type="text" class="input font-mono text-sm" maxlength="64" />
          </div>

          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.basePort') }}</label>
            <input v-model.number="form.base_port" type="number" class="input" min="1024" max="65535" />
          </div>
          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.maxPorts') }}</label>
            <input v-model.number="form.max_ports" type="number" class="input" min="1" max="500" />
          </div>

          <div>
            <label class="label">{{ t('admin.proxies.subscriptions.form.syncInterval') }}</label>
            <input v-model.number="form.sync_interval_sec" type="number" class="input" min="60" max="86400" />
            <p class="mt-1 text-xs text-gray-400">
              {{ t('admin.proxies.subscriptions.form.syncIntervalHint') }}
            </p>
          </div>
          <div class="flex items-end pb-1">
            <label class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="form.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />
              {{ t('admin.proxies.subscriptions.form.enabled') }}
            </label>
          </div>

          <div class="sm:col-span-2">
            <label class="label">{{ t('admin.proxies.subscriptions.form.nodeAllow') }}</label>
            <input
              v-model="form.node_allow_text"
              type="text"
              class="input"
              :placeholder="t('admin.proxies.subscriptions.form.nodeAllowPlaceholder')"
            />
            <p class="mt-1 text-xs text-gray-400">
              {{ t('admin.proxies.subscriptions.form.nodeAllowHint') }}
            </p>
          </div>

          <div class="sm:col-span-2 space-y-2">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <label class="label mb-0">{{ t('admin.proxies.subscriptions.form.nodeWhitelist') }}</label>
              <div class="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="previewLoading"
                  @click="loadPreviewNodes"
                >
                  <Icon name="refresh" size="sm" :class="previewLoading ? 'animate-spin' : ''" />
                  <span class="ml-1">{{
                    previewLoading
                      ? t('admin.proxies.subscriptions.form.loadingNodes')
                      : t('admin.proxies.subscriptions.form.loadNodes')
                  }}</span>
                </button>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="previewNodes.length === 0"
                  @click="selectAllPreviewNodes"
                >
                  {{ t('admin.proxies.subscriptions.form.selectAllNodes') }}
                </button>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="selectedIdentities.size === 0"
                  @click="clearPreviewSelection"
                >
                  {{ t('admin.proxies.subscriptions.form.clearNodeSelection') }}
                </button>
              </div>
            </div>
            <p class="text-xs text-gray-400">
              {{ t('admin.proxies.subscriptions.form.nodeWhitelistHint') }}
            </p>
            <div class="flex flex-wrap items-center gap-3 text-xs text-gray-500 dark:text-gray-400">
              <span>
                {{
                  t('admin.proxies.subscriptions.form.selectedCount', {
                    selected: selectedIdentities.size,
                    max: form.max_ports || 0
                  })
                }}
              </span>
              <input
                v-if="previewNodes.length > 0 || missingSelectedIdentities.length > 0"
                v-model="nodeSearch"
                type="text"
                class="input input-sm max-w-xs"
                :placeholder="t('admin.proxies.subscriptions.form.nodeSearchPlaceholder')"
              />
            </div>
            <div
              v-if="missingSelectedIdentities.length > 0"
              class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/40 dark:bg-amber-900/20 dark:text-amber-200"
            >
              <div class="mb-1 font-medium">
                {{ t('admin.proxies.subscriptions.form.missingFromPreview') }}
              </div>
              <div class="flex flex-wrap gap-2">
                <label
                  v-for="id in missingSelectedIdentities"
                  :key="'missing-' + id"
                  class="inline-flex max-w-full items-center gap-1 truncate"
                >
                  <input
                    type="checkbox"
                    class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600"
                    :checked="selectedIdentities.has(id)"
                    @change="onIdentityCheckbox(id, $event)"
                  />
                  <code class="truncate font-mono">{{ id }}</code>
                </label>
              </div>
            </div>
            <div
              class="max-h-56 overflow-auto rounded-lg border border-gray-200 dark:border-dark-600"
            >
              <div
                v-if="previewLoading"
                class="px-3 py-6 text-center text-xs text-gray-400"
              >
                {{ t('admin.proxies.subscriptions.form.loadingNodes') }}
              </div>
              <div
                v-else-if="previewNodes.length === 0"
                class="px-3 py-6 text-center text-xs text-gray-400"
              >
                {{ t('admin.proxies.subscriptions.form.noNodesLoaded') }}
              </div>
              <table v-else class="min-w-full divide-y divide-gray-100 text-xs dark:divide-dark-700">
                <tbody class="divide-y divide-gray-50 dark:divide-dark-800">
                  <tr
                    v-for="node in filteredPreviewNodes"
                    :key="node.identity"
                    class="hover:bg-gray-50 dark:hover:bg-dark-800/60"
                  >
                    <td class="w-8 px-3 py-2">
                      <input
                        type="checkbox"
                        class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600"
                        :checked="selectedIdentities.has(node.identity)"
                        @change="onIdentityCheckbox(node.identity, $event)"
                      />
                    </td>
                    <td class="px-2 py-2 font-medium text-gray-900 dark:text-gray-100">
                      {{ node.name }}
                    </td>
                    <td class="px-2 py-2 text-gray-500">{{ node.type }}</td>
                    <td class="px-2 py-2 font-mono text-gray-500">
                      {{ node.server }}{{ node.port ? ':' + node.port : '' }}
                    </td>
                    <td class="px-2 py-2 text-right">
                      <div class="flex items-center justify-end gap-2">
                        <button
                          type="button"
                          class="btn btn-xs btn-secondary"
                          :disabled="testingNodeIdentities.has(nodeKey(node))"
                          :title="t('admin.proxies.subscriptions.form.testNode')"
                          @click="testNode(node)"
                        >
                          <svg
                            v-if="testingNodeIdentities.has(nodeKey(node))"
                            class="h-3 w-3 animate-spin"
                            fill="none"
                            viewBox="0 0 24 24"
                          >
                            <circle
                              class="opacity-25"
                              cx="12"
                              cy="12"
                              r="10"
                              stroke="currentColor"
                              stroke-width="4"
                            ></circle>
                            <path
                              class="opacity-75"
                              fill="currentColor"
                              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                            ></path>
                          </svg>
                          <Icon v-else name="play" size="xs" />
                        </button>
                        <span v-if="getNodeResult(node)" class="text-xs">
                          <span
                            v-if="getNodeResult(node)!.success"
                            class="text-emerald-600 dark:text-emerald-400"
                          >
                            {{ getNodeResult(node)!.latency_ms }}ms
                            <span
                              v-if="isNodeResultFresh(node)"
                              class="ml-1 text-[10px] text-gray-400"
                            >
                              ({{ t('admin.proxies.pools.testCached') }})
                            </span>
                          </span>
                          <span
                            v-else
                            class="cursor-pointer text-red-500 dark:text-red-400"
                            :title="getNodeResult(node)!.message"
                            @click.stop="openNodeFailDetail(node)"
                          >
                            {{ t('admin.proxies.subscriptions.form.testNodeFail') }}
                          </span>
                        </span>
                      </div>
                    </td>
                  </tr>
                  <tr v-if="filteredPreviewNodes.length === 0">
                    <td colspan="5" class="px-3 py-4 text-center text-gray-400">
                      {{ t('admin.proxies.subscriptions.form.noNodesLoaded') }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-600">
          <button type="button" class="btn btn-secondary" @click="closeForm">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" class="btn btn-primary" :disabled="submitting">
            {{ submitting ? t('admin.proxies.saving') : t('common.save') }}
          </button>
        </div>
      </form>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.proxies.subscriptions.delete')"
      :message="t('admin.proxies.subscriptions.deleteConfirm', { name: deleting?.name || '' })"
      :confirm-text="t('common.delete')"
      danger
      @confirm="handleDelete"
      @cancel="showDeleteDialog = false"
    />

    <BaseDialog
      :show="showNodeFailModal"
      :title="t('admin.proxies.subscriptions.form.testNodeFail')"
      width="narrow"
      :z-index="80"
      @close="showNodeFailModal = false"
    >
      <div v-if="nodeFailDetail" class="space-y-2 text-sm">
        <div class="font-medium text-gray-800 dark:text-gray-100">{{ nodeFailDetail.name }}</div>
        <div class="font-mono text-xs text-gray-500">{{ nodeFailDetail.server }}:{{ nodeFailDetail.port }}</div>
        <pre
          class="max-h-56 overflow-auto whitespace-pre-wrap break-words rounded border border-gray-200 bg-gray-50 p-3 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200"
        >{{ nodeFailDetail.message || t('admin.proxies.pools.grokDetailNoReason') }}</pre>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showNodeFailModal = false">
          {{ t('common.close') }}
        </button>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  ProxySubscription,
  ProxySubscriptionCreateParams,
  ProxySubscriptionEngineStatus,
  ProxySubscriptionPreviewNode,
  ProxySubscriptionSourceType,
  ProxySubscriptionUpdateParams
} from '@/api/admin/proxySubscriptions'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import {
  getFreshNodeTest,
  isNodeTestFresh,
  nodeTestCacheKey,
  setNodeTest,
  type NodeTestCacheEntry
} from '@/utils/nodeTestCache'

const emit = defineEmits<{
  synced: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<ProxySubscription[]>([])
const loading = ref(false)
const engineLoading = ref(false)
const engineStatus = ref<ProxySubscriptionEngineStatus | null>(null)
const searchQuery = ref('')
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})

const showForm = ref(false)
const editing = ref<ProxySubscription | null>(null)
const submitting = ref(false)
const syncingIds = ref<Set<number>>(new Set())
const togglingIds = ref<Set<number>>(new Set())

const showDeleteDialog = ref(false)
const deleting = ref<ProxySubscription | null>(null)
const deletingBusy = ref(false)

const form = reactive({
  name: '',
  enabled: true,
  source_type: 'url' as ProxySubscriptionSourceType,
  subscription_url: '',
  inline_body: '',
  name_prefix: 'sidecar-a-',
  protocol: 'socks5',
  bind_address: '127.0.0.1',
  base_port: 17890,
  max_ports: 8,
  sync_interval_sec: 3600,
  node_allow_text: ''
})

const previewNodes = ref<ProxySubscriptionPreviewNode[]>([])
const previewLoading = ref(false)
const selectedIdentities = ref<Set<string>>(new Set())
const nodeSearch = ref('')
const nodeTestResults = reactive<Record<string, NodeTestCacheEntry>>({})
const testingNodeIdentities = reactive(new Set<string>())
const showNodeFailModal = ref(false)
const nodeFailDetail = ref<{ name: string; server: string; port: string; message: string } | null>(null)

function nodeKey(node: { identity?: string; server?: string; port?: string | number }) {
  return nodeTestCacheKey(node.identity || '', node.server, node.port)
}

function getNodeResult(node: { identity?: string; server?: string; port?: string | number }) {
  const key = nodeKey(node)
  return nodeTestResults[key] || getFreshNodeTest(key)
}

function isNodeResultFresh(node: { identity?: string; server?: string; port?: string | number }) {
  return isNodeTestFresh(nodeKey(node))
}

function openNodeFailDetail(node: ProxySubscriptionPreviewNode) {
  const result = getNodeResult(node)
  if (!result || result.success) return
  nodeFailDetail.value = {
    name: node.name || node.identity,
    server: node.server,
    port: String(node.port || ''),
    message: result.message || ''
  }
  showNodeFailModal.value = true
}

const filteredPreviewNodes = computed(() => {
  const q = nodeSearch.value.trim().toLowerCase()
  if (!q) return previewNodes.value
  return previewNodes.value.filter((n) => {
    const hay = `${n.name} ${n.type} ${n.server} ${n.port} ${n.identity}`.toLowerCase()
    return hay.includes(q)
  })
})

const missingSelectedIdentities = computed(() => {
  if (selectedIdentities.value.size === 0) return [] as string[]
  const present = new Set(previewNodes.value.map((n) => n.identity))
  return [...selectedIdentities.value].filter((id) => !present.has(id))
})

const sourceTypeOptions = computed(() => [
  { value: 'url', label: t('admin.proxies.subscriptions.sourceUrl') },
  { value: 'inline', label: t('admin.proxies.subscriptions.sourceInline') }
])

const protocolOptions = computed(() => [
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'socks5h', label: 'SOCKS5H' },
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' }
])

const engineBannerClass = computed(() => {
  if (!engineStatus.value) {
    return 'border-gray-200 bg-gray-50 text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
  }
  if (!engineStatus.value.binary_found) {
    return 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/40 dark:bg-amber-900/20 dark:text-amber-200'
  }
  if (engineStatus.value.running_count > 0) {
    return 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/40 dark:bg-emerald-900/20 dark:text-emerald-200'
  }
  return 'border-blue-200 bg-blue-50 text-blue-800 dark:border-blue-900/40 dark:bg-blue-900/20 dark:text-blue-200'
})

const engineBannerTitle = computed(() => {
  if (!engineStatus.value) return t('admin.proxies.subscriptions.engineUnknown')
  if (!engineStatus.value.binary_found) return t('admin.proxies.subscriptions.engineMissing')
  if (engineStatus.value.running_count > 0) return t('admin.proxies.subscriptions.engineRunning')
  return t('admin.proxies.subscriptions.engineIdle')
})

let searchTimeout: ReturnType<typeof setTimeout> | undefined
let abortController: AbortController | null = null

function sourceHint(row: ProxySubscription): string {
  if (row.source_type === 'inline') {
    return row.has_inline_body
      ? t('admin.proxies.subscriptions.hasInlineBody')
      : t('admin.proxies.subscriptions.noInlineBody')
  }
  return row.subscription_url_masked || '-'
}

function syncStatusBadge(status: string): string {
  switch (status) {
    case 'ok':
      return 'badge-success'
    case 'error':
      return 'badge-danger'
    case 'running':
      return 'badge-primary'
    default:
      return 'badge-gray'
  }
}

function syncStatusLabel(status: string): string {
  switch (status) {
    case 'ok':
      return t('admin.proxies.subscriptions.statusOk')
    case 'error':
      return t('admin.proxies.subscriptions.statusError')
    case 'running':
      return t('admin.proxies.subscriptions.statusRunning')
    default:
      return t('admin.proxies.subscriptions.statusIdle')
  }
}

function parseAllowList(text: string): string[] {
  return text
    .split(/[,，\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function resetForm() {
  form.name = ''
  form.enabled = true
  form.source_type = 'url'
  form.subscription_url = ''
  form.inline_body = ''
  form.name_prefix = 'sidecar-a-'
  form.protocol = 'socks5'
  form.bind_address = '127.0.0.1'
  form.base_port = 17890
  form.max_ports = 8
  form.sync_interval_sec = 3600
  form.node_allow_text = ''
  previewNodes.value = []
  selectedIdentities.value = new Set()
  nodeSearch.value = ''
}

function openCreate() {
  editing.value = null
  resetForm()
  showForm.value = true
}

function openEdit(row: ProxySubscription) {
  editing.value = row
  form.name = row.name
  form.enabled = row.enabled
  form.source_type = (row.source_type === 'inline' ? 'inline' : 'url') as ProxySubscriptionSourceType
  form.subscription_url = ''
  form.inline_body = ''
  form.name_prefix = row.name_prefix
  form.protocol = row.protocol || 'socks5'
  form.bind_address = row.bind_address || '127.0.0.1'
  form.base_port = row.base_port
  form.max_ports = row.max_ports
  form.sync_interval_sec = row.sync_interval_sec
  form.node_allow_text = (row.node_allow_contains || []).join(', ')
  previewNodes.value = []
  nodeSearch.value = ''
  selectedIdentities.value = new Set(row.node_identity_allowlist || [])
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editing.value = null
}

async function loadList() {
  abortController?.abort()
  abortController = new AbortController()
  loading.value = true
  try {
    const res = await adminAPI.proxySubscriptions.list(
      {
        page: pagination.page,
        page_size: pagination.page_size,
        search: searchQuery.value.trim() || undefined
      },
      { signal: abortController.signal }
    )
    items.value = res.items || []
    pagination.total = res.total || 0
    pagination.pages = res.pages || 0
    pagination.page = res.page || pagination.page
    pagination.page_size = res.page_size || pagination.page_size
  } catch (error: any) {
    if (error?.name === 'CanceledError' || error?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.subscriptions.failedToLoad')))
  } finally {
    loading.value = false
  }
}

async function loadEngineStatus() {
  engineLoading.value = true
  try {
    engineStatus.value = await adminAPI.proxySubscriptions.engineStatus()
  } catch (error: any) {
    engineStatus.value = null
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.subscriptions.failedToLoadEngine')))
  } finally {
    engineLoading.value = false
  }
}

function reload() {
  void loadList()
  void loadEngineStatus()
}

function handleSearch() {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    void loadList()
  }, 300)
}

function onPageChange(page: number) {
  pagination.page = page
  void loadList()
}

function onPageSizeChange(size: number) {
  pagination.page_size = size
  pagination.page = 1
  void loadList()
}

function toggleIdentity(identity: string, checked: boolean) {
  const next = new Set(selectedIdentities.value)
  if (checked) next.add(identity)
  else next.delete(identity)
  selectedIdentities.value = next
}

function onIdentityCheckbox(identity: string, event: Event) {
  const target = event.target as HTMLInputElement | null
  toggleIdentity(identity, !!target?.checked)
}

function selectAllPreviewNodes() {
  const next = new Set(selectedIdentities.value)
  for (const n of filteredPreviewNodes.value) next.add(n.identity)
  selectedIdentities.value = next
}

function clearPreviewSelection() {
  selectedIdentities.value = new Set()
}

async function testNode(node: ProxySubscriptionPreviewNode, force = false) {
  const key = nodeKey(node)
  if (testingNodeIdentities.has(key)) return
  if (!force && isNodeTestFresh(key)) {
    const cached = getFreshNodeTest(key)
    if (cached) nodeTestResults[key] = cached
    return
  }
  testingNodeIdentities.add(key)
  try {
    const res = await adminAPI.proxySubscriptions.testNode(editing.value?.id || 0, node.server, node.port)
    setNodeTest(key, {
      success: !!res.success,
      latency_ms: res.latency_ms,
      message: res.message || ''
    })
    const cached = getFreshNodeTest(key)
    if (cached) nodeTestResults[key] = cached
  } catch (error: any) {
    setNodeTest(key, {
      success: false,
      message: extractApiErrorMessage(error, t('admin.proxies.subscriptions.form.testNodeFail'))
    })
    const cached = getFreshNodeTest(key)
    if (cached) nodeTestResults[key] = cached
  } finally {
    testingNodeIdentities.delete(key)
  }
}

async function loadPreviewNodes() {
  previewLoading.value = true
  try {
    const allow = parseAllowList(form.node_allow_text)
    let res
    if (editing.value) {
      res = await adminAPI.proxySubscriptions.previewNodes(editing.value.id)
    } else {
      if (form.source_type === 'url' && !form.subscription_url.trim()) {
        appStore.showError(t('admin.proxies.subscriptions.urlRequired'))
        return
      }
      if (form.source_type === 'inline' && !form.inline_body.trim()) {
        appStore.showError(t('admin.proxies.subscriptions.inlineRequired'))
        return
      }
      res = await adminAPI.proxySubscriptions.previewNodesDraft({
        source_type: form.source_type,
        subscription_url: form.source_type === 'url' ? form.subscription_url.trim() : undefined,
        inline_body: form.source_type === 'inline' ? form.inline_body : undefined,
        node_allow_contains: allow
      })
    }
    previewNodes.value = res.nodes || []
    // Restore 5-minute connectivity cache so reopening the form doesn't retest.
    Object.keys(nodeTestResults).forEach((k) => delete nodeTestResults[k])
    for (const node of previewNodes.value) {
      const key = nodeKey(node)
      const cached = getFreshNodeTest(key)
      if (cached) nodeTestResults[key] = cached
    }
    // Keep existing selection; if server returned selected_identities on edit and local empty, use it.
    if (selectedIdentities.value.size === 0 && (res.selected_identities || []).length > 0) {
      selectedIdentities.value = new Set(res.selected_identities)
    }
  } catch (error: any) {
    appStore.showError(
      extractApiErrorMessage(error, t('admin.proxies.subscriptions.form.nodesLoadFailed'))
    )
  } finally {
    previewLoading.value = false
  }
}

async function submitForm() {
  const name = form.name.trim()
  if (!name) {
    appStore.showError(t('admin.proxies.subscriptions.nameRequired'))
    return
  }
  if (!form.name_prefix.trim().startsWith('sidecar-')) {
    appStore.showError(t('admin.proxies.subscriptions.prefixInvalid'))
    return
  }
  if (!editing.value) {
    if (form.source_type === 'url' && !form.subscription_url.trim()) {
      appStore.showError(t('admin.proxies.subscriptions.urlRequired'))
      return
    }
    if (form.source_type === 'inline' && !form.inline_body.trim()) {
      appStore.showError(t('admin.proxies.subscriptions.inlineRequired'))
      return
    }
  }

  submitting.value = true
  try {
    const allow = parseAllowList(form.node_allow_text)
    if (editing.value) {
      const params: ProxySubscriptionUpdateParams = {
        name,
        enabled: form.enabled,
        source_type: form.source_type,
        name_prefix: form.name_prefix.trim(),
        protocol: form.protocol as ProxySubscriptionCreateParams['protocol'],
        bind_address: form.bind_address.trim() || '127.0.0.1',
        base_port: Number(form.base_port),
        max_ports: Number(form.max_ports),
        sync_interval_sec: Number(form.sync_interval_sec),
        node_allow_contains: allow,
        node_identity_allowlist: [...selectedIdentities.value]
      }
      if (form.source_type === 'url' && form.subscription_url.trim()) {
        params.subscription_url = form.subscription_url.trim()
      }
      if (form.source_type === 'inline' && form.inline_body.trim()) {
        params.inline_body = form.inline_body
      }
      await adminAPI.proxySubscriptions.update(editing.value.id, params)
      appStore.showSuccess(t('admin.proxies.subscriptions.updated'))
    } else {
      const params: ProxySubscriptionCreateParams = {
        name,
        enabled: form.enabled,
        source_type: form.source_type,
        name_prefix: form.name_prefix.trim(),
        protocol: form.protocol as ProxySubscriptionCreateParams['protocol'],
        bind_address: form.bind_address.trim() || '127.0.0.1',
        base_port: Number(form.base_port),
        max_ports: Number(form.max_ports),
        sync_interval_sec: Number(form.sync_interval_sec),
        node_allow_contains: allow,
        node_identity_allowlist: [...selectedIdentities.value]
      }
      if (form.source_type === 'url') {
        params.subscription_url = form.subscription_url.trim()
      } else {
        params.inline_body = form.inline_body
      }
      await adminAPI.proxySubscriptions.create(params)
      appStore.showSuccess(t('admin.proxies.subscriptions.created'))
    }
    closeForm()
    await loadList()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.subscriptions.failedToSave')))
  } finally {
    submitting.value = false
  }
}

async function toggleEnabled(row: ProxySubscription) {
  if (togglingIds.value.has(row.id)) return
  const next = new Set(togglingIds.value)
  next.add(row.id)
  togglingIds.value = next
  try {
    const updated = await adminAPI.proxySubscriptions.update(row.id, { enabled: !row.enabled })
    const idx = items.value.findIndex((x) => x.id === row.id)
    if (idx >= 0) items.value[idx] = updated
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.subscriptions.failedToSave')))
  } finally {
    const done = new Set(togglingIds.value)
    done.delete(row.id)
    togglingIds.value = done
  }
}

async function handleSync(row: ProxySubscription) {
  if (syncingIds.value.has(row.id)) return
  const next = new Set(syncingIds.value)
  next.add(row.id)
  syncingIds.value = next
  try {
    const result = await adminAPI.proxySubscriptions.sync(row.id)
    const parts = [
      t('admin.proxies.subscriptions.syncSummary', {
        created: result.created,
        updated: result.updated,
        deleted: result.deleted,
        skipped: result.skipped,
        desired: result.desired_count
      })
    ]
    if (result.engine_skipped) {
      parts.push(t('admin.proxies.subscriptions.engineSkipped'))
    } else if (result.engine_running) {
      parts.push(t('admin.proxies.subscriptions.engineRunningShort'))
    }
    if (result.message) parts.push(result.message)
    appStore.showSuccess(parts.join(' · '))
    emit('synced')
    await Promise.all([loadList(), loadEngineStatus()])
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.subscriptions.failedToSync')))
    await loadList()
  } finally {
    const done = new Set(syncingIds.value)
    done.delete(row.id)
    syncingIds.value = done
  }
}

function confirmDelete(row: ProxySubscription) {
  deleting.value = row
  showDeleteDialog.value = true
}

async function handleDelete() {
  if (!deleting.value) return
  deletingBusy.value = true
  try {
    await adminAPI.proxySubscriptions.del(deleting.value.id)
    appStore.showSuccess(t('admin.proxies.subscriptions.deleted'))
    showDeleteDialog.value = false
    deleting.value = null
    await Promise.all([loadList(), loadEngineStatus()])
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.subscriptions.failedToDelete')))
  } finally {
    deletingBusy.value = false
  }
}

onMounted(() => {
  reload()
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})

defineExpose({ reload })
</script>
