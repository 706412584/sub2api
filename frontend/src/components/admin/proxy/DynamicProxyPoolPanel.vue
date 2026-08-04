<template>
  <div class="flex min-h-0 flex-1 flex-col overflow-hidden">
    <div class="mb-4 flex items-center justify-between">
      <Input v-model="search" :placeholder="t('admin.proxies.searchProxies')" class="w-64" @input="loadPools" />
      <button @click="showCreateModal = true" class="btn btn-primary">
        <Icon name="plus" size="md" class="mr-2" />
        {{ t('admin.proxies.createProxy') }}
      </button>
    </div>

    <DataTable
      :columns="columns"
      :data="pools"
      :loading="loading"
      @sort="onSort"
    >
      <template #cell-name="{ row }">
        <div class="flex items-center gap-2">
          <span class="font-medium">{{ row.name }}</span>
          <span v-if="!row.enabled" class="badge badge-warning text-xs">disabled</span>
        </div>
      </template>
      <template #cell-alive_count="{ row }">
        <span :class="row.alive_count >= row.min_alive ? 'text-green-600' : 'text-orange-600'">
          {{ row.alive_count }} / {{ row.min_alive }}
        </span>
      </template>
      <template #cell-last_extract="{ row }">
        <div class="text-xs">
          <div v-if="row.last_extract_at">{{ formatDateTime(row.last_extract_at) }}</div>
          <span v-if="row.last_extract_status === 'success'" class="text-green-600">success</span>
          <span v-else-if="row.last_extract_status === 'error'" class="text-red-600" :title="row.last_extract_error">error</span>
          <span v-else class="text-gray-400">-</span>
        </div>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center gap-2">
          <button class="btn btn-sm btn-secondary" @click="triggerExtract(row)" :disabled="extractingId === row.id">
            <Icon name="refresh" size="sm" />
            {{ extractingId === row.id ? '...' : t('admin.proxies.testConnection') }}
          </button>
          <button class="btn btn-sm btn-secondary" @click="editPool(row)">
            <Icon name="edit" size="sm" />
          </button>
          <button class="btn btn-sm btn-danger" @click="confirmDelete(row)">
            <Icon name="trash" size="sm" />
          </button>
        </div>
      </template>
    </DataTable>

    <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="handlePageChange" />

    <!-- Create Modal -->
    <BaseDialog :show="showCreateModal" :title="t('admin.proxies.createProxy')" width="wide" @close="showCreateModal = false">
      <div class="grid grid-cols-2 gap-4">
        <div class="col-span-2">
          <label class="input-label">Name *</label>
          <Input v-model="createForm.name" placeholder="My Pool" />
        </div>
        <div class="col-span-2">
          <label class="input-label">Extract URL *</label>
          <Input v-model="createForm.extract_url" placeholder="https://..." type="url" />
        </div>
        <div>
          <label class="input-label">Protocol</label>
          <Select v-model="createForm.protocol" :options="protocolOptions" />
        </div>
        <div>
          <label class="input-label">Auth Mode</label>
          <Select v-model="createForm.auth_mode" :options="authModeOptions" />
        </div>
        <div v-if="createForm.auth_mode === 'fixed'">
          <label class="input-label">Username</label>
          <Input v-model="createForm.username" />
        </div>
        <div v-if="createForm.auth_mode === 'fixed'">
          <label class="input-label">Password</label>
          <Input v-model="createForm.password" type="password" />
        </div>
        <div>
          <label class="input-label">Response Format</label>
          <Select v-model="createForm.response_format" :options="formatOptions" />
        </div>
        <div v-if="createForm.response_format === 'json'">
          <label class="input-label">IP Field Path</label>
          <Input v-model="createForm.ip_field_path" placeholder="ip" />
        </div>
        <div v-if="createForm.response_format === 'json'">
          <label class="input-label">Port Field Path</label>
          <Input v-model="createForm.port_field_path" placeholder="port" />
        </div>
        <div>
          <label class="input-label">Line Separator</label>
          <Input v-model="createForm.line_separator" placeholder="\r\n" />
        </div>
        <div>
          <label class="input-label">Refresh Interval (sec)</label>
          <Input v-model.number="createForm.refresh_interval_sec" type="number" min="60" />
        </div>
        <div>
          <label class="input-label">IP Duration (sec)</label>
          <Input v-model.number="createForm.ip_duration_sec" type="number" min="30" />
        </div>
        <div>
          <label class="input-label">Extract Count</label>
          <Input v-model.number="createForm.extract_count" type="number" min="1" />
        </div>
        <div>
          <label class="input-label">Min Alive</label>
          <Input v-model.number="createForm.min_alive" type="number" min="1" />
        </div>
      </div>
      <template #actions>
        <button class="btn btn-secondary" @click="showCreateModal = false">{{ t('common.cancel') }}</button>
        <button class="btn btn-primary" @click="createPool" :disabled="saving">{{ saving ? '...' : t('common.save') }}</button>
      </template>
    </BaseDialog>

    <!-- Edit Modal -->
    <BaseDialog :show="showEditModal" :title="t('admin.proxies.editProxy')" width="wide" @close="showEditModal = false">
      <div class="grid grid-cols-2 gap-4">
        <div class="col-span-2">
          <label class="input-label">Name</label>
          <Input v-model="editForm.name" />
        </div>
        <div class="col-span-2">
          <label class="input-label">Enabled</label>
          <input type="checkbox" v-model="editForm.enabled" class="toggle" />
        </div>
        <div class="col-span-2">
          <label class="input-label">Extract URL</label>
          <Input v-model="editForm.extract_url" type="url" />
        </div>
        <div>
          <label class="input-label">Protocol</label>
          <Select v-model="editForm.protocol" :options="protocolOptions" />
        </div>
        <div>
          <label class="input-label">Auth Mode</label>
          <Select v-model="editForm.auth_mode" :options="authModeOptions" />
        </div>
        <div v-if="editForm.auth_mode === 'fixed'">
          <label class="input-label">Username</label>
          <Input v-model="editForm.username" />
        </div>
        <div v-if="editForm.auth_mode === 'fixed'">
          <label class="input-label">Password</label>
          <Input v-model="editForm.password" type="password" />
        </div>
        <div>
          <label class="input-label">Response Format</label>
          <Select v-model="editForm.response_format" :options="formatOptions" />
        </div>
        <div v-if="editForm.response_format === 'json'">
          <label class="input-label">IP Field</label>
          <Input v-model="editForm.ip_field_path" placeholder="ip" />
        </div>
        <div v-if="editForm.response_format === 'json'">
          <label class="input-label">Port Field</label>
          <Input v-model="editForm.port_field_path" placeholder="port" />
        </div>
        <div>
          <label class="input-label">Refresh Interval</label>
          <Input v-model.number="editForm.refresh_interval_sec" type="number" min="60" />
        </div>
        <div>
          <label class="input-label">IP Duration</label>
          <Input v-model.number="editForm.ip_duration_sec" type="number" min="30" />
        </div>
        <div>
          <label class="input-label">Extract Count</label>
          <Input v-model.number="editForm.extract_count" type="number" min="1" />
        </div>
        <div>
          <label class="input-label">Min Alive</label>
          <Input v-model.number="editForm.min_alive" type="number" min="1" />
        </div>
      </div>
      <template #actions>
        <button class="btn btn-secondary" @click="showEditModal = false">{{ t('common.cancel') }}</button>
        <button class="btn btn-primary" @click="saveEdit" :disabled="saving">{{ saving ? '...' : t('common.save') }}</button>
      </template>
    </BaseDialog>

    <!-- Delete Confirm -->
    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.proxies.deleteProxy')"
      :message="deleteMessage"
      @confirm="doDelete"
      @cancel="showDeleteConfirm = false"
    />

    <!-- Extract Result -->
    <BaseDialog :show="showResultModal" title="Extract Result" width="narrow" @close="showResultModal = false">
      <div v-if="extractResult" class="space-y-2">
        <div>Created: <strong>{{ extractResult.created }}</strong></div>
        <div>Failed: <strong>{{ extractResult.failed }}</strong></div>
        <div>Alive: <strong>{{ extractResult.alive_count }}</strong></div>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { dynamicProxyPoolsAPI } from '@/api/admin/dynamicProxyPools'
import type { DynamicProxyPool } from '@/types'
import type { Column } from '@/components/common/types'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Input from '@/components/common/Input.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const pools = ref<DynamicProxyPool[]>([])
const loading = ref(false)
const saving = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const search = ref('')
const sortBy = ref('id')
const sortOrder = ref('desc')
const extractingId = ref<number | null>(null)

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteConfirm = ref(false)
const showResultModal = ref(false)
const selectedPool = ref<DynamicProxyPool | null>(null)
const extractResult = ref<any>(null)
const deleteMessage = ref('')

const protocolOptions = [
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'socks5h', label: 'SOCKS5H' },
]

const authModeOptions = [
  { value: 'none', label: 'None' },
  { value: 'fixed', label: 'Fixed' },
  { value: 'from_response', label: 'From Response' },
]

const formatOptions = [
  { value: 'txt', label: 'TXT' },
  { value: 'json', label: 'JSON' },
]

const columns: Column[] = [
  { key: 'name', label: 'Name', sortable: true },
  { key: 'protocol', label: 'Protocol' },
  { key: 'alive_count', label: 'Alive', sortable: true },
  { key: 'refresh_interval_sec', label: 'Interval' },
  { key: 'ip_duration_sec', label: 'Duration' },
  { key: 'last_extract', label: 'Last Extract' },
  { key: 'actions', label: 'Actions' },
]

const createForm = reactive({
  name: '',
  extract_url: '',
  protocol: 'http',
  auth_mode: 'none',
  username: '',
  password: '',
  response_format: 'txt',
  line_separator: '\\r\\n',
  ip_field_path: '',
  port_field_path: '',
  refresh_interval_sec: 300,
  ip_duration_sec: 300,
  extract_count: 1,
  min_alive: 1,
})

const editForm = reactive({
  name: '',
  enabled: true,
  extract_url: '',
  protocol: 'http',
  auth_mode: 'none',
  username: '',
  password: '',
  response_format: 'txt',
  line_separator: '\\r\\n',
  ip_field_path: '',
  port_field_path: '',
  refresh_interval_sec: 300,
  ip_duration_sec: 300,
  extract_count: 1,
  min_alive: 1,
})

function formatDateTime(ts: string) {
  const d = new Date(ts)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function loadPools() {
  loading.value = true
  try {
    const res = await dynamicProxyPoolsAPI.list({
      page: page.value,
      page_size: pageSize.value,
      search: search.value || undefined,
    })
    pools.value = res.items
    total.value = res.total
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load pools')
  } finally {
    loading.value = false
  }
}

function onSort(key: string, order: string) {
  sortBy.value = key
  sortOrder.value = order
  loadPools()
}

function handlePageChange(p: number) {
  page.value = p
  loadPools()
}

async function createPool() {
  if (!createForm.name || !createForm.extract_url) return
  saving.value = true
  try {
    await dynamicProxyPoolsAPI.create({ ...createForm })
    showCreateModal.value = false
    resetCreateForm()
    loadPools()
    appStore.showSuccess('Pool created')
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to create pool')
  } finally {
    saving.value = false
  }
}

function resetCreateForm() {
  createForm.name = ''
  createForm.extract_url = ''
  createForm.protocol = 'http'
  createForm.auth_mode = 'none'
  createForm.username = ''
  createForm.password = ''
  createForm.response_format = 'txt'
  createForm.line_separator = '\\r\\n'
  createForm.ip_field_path = ''
  createForm.port_field_path = ''
  createForm.refresh_interval_sec = 300
  createForm.ip_duration_sec = 300
  createForm.extract_count = 1
  createForm.min_alive = 1
}

function editPool(pool: DynamicProxyPool) {
  selectedPool.value = pool
  editForm.name = pool.name
  editForm.enabled = pool.enabled
  editForm.extract_url = pool.extract_url
  editForm.protocol = pool.protocol
  editForm.auth_mode = pool.auth_mode
  editForm.username = pool.username
  editForm.password = ''
  editForm.response_format = pool.response_format
  editForm.line_separator = pool.line_separator
  editForm.ip_field_path = pool.ip_field_path
  editForm.port_field_path = pool.port_field_path
  editForm.refresh_interval_sec = pool.refresh_interval_sec
  editForm.ip_duration_sec = pool.ip_duration_sec
  editForm.extract_count = pool.extract_count
  editForm.min_alive = pool.min_alive
  showEditModal.value = true
}

async function saveEdit() {
  if (!selectedPool.value) return
  saving.value = true
  try {
    await dynamicProxyPoolsAPI.update(selectedPool.value.id, { ...editForm })
    showEditModal.value = false
    loadPools()
    appStore.showSuccess('Pool updated')
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to update pool')
  } finally {
    saving.value = false
  }
}

function confirmDelete(pool: DynamicProxyPool) {
  selectedPool.value = pool
  deleteMessage.value = `Delete pool "${pool.name}"? Managed proxies will also be removed.`
  showDeleteConfirm.value = true
}

async function doDelete() {
  if (!selectedPool.value) return
  try {
    await dynamicProxyPoolsAPI.delete(selectedPool.value.id)
    showDeleteConfirm.value = false
    loadPools()
    appStore.showSuccess('Pool deleted')
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to delete pool')
  }
}

async function triggerExtract(pool: DynamicProxyPool) {
  extractingId.value = pool.id
  try {
    const result = await dynamicProxyPoolsAPI.extract(pool.id)
    extractResult.value = result
    showResultModal.value = true
    loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || 'Extract failed')
  } finally {
    extractingId.value = null
  }
}

onMounted(loadPools)
</script>