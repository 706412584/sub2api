<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.dataImportTitle')"
    width="wide"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="import-data-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="grid grid-cols-2 gap-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-800">
        <button
          type="button"
          :class="['rounded-md px-3 py-2 text-sm font-medium transition-colors', importMode === 'sub2api' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 dark:text-gray-400']"
          @click="setImportMode('sub2api')"
        >
          Sub2API
        </button>
        <button
          type="button"
          :class="['rounded-md px-3 py-2 text-sm font-medium transition-colors', importMode === 'kiro' ? 'bg-white text-indigo-700 shadow-sm dark:bg-dark-700 dark:text-indigo-300' : 'text-gray-500 dark:text-gray-400']"
          @click="setImportMode('kiro')"
        >
          {{ t('admin.accounts.kiroImportMode') }}
        </button>
      </div>
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ importMode === 'kiro' ? t('admin.accounts.kiroImportHint') : t('admin.accounts.dataImportHint') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ t('admin.accounts.dataImportWarning') }}
      </div>

      <div>
        <GroupSelector v-model="selectedGroupIds" :groups="groups" searchable />
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.importPreGroupHint') }}
        </p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.importPreProxy') }}</label>
        <ProxySelector v-model="selectedProxyId" :proxies="proxies" />
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.importPreProxyHint') }}
        </p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex items-center justify-between gap-3 rounded-lg border border-dashed px-4 py-3 transition-colors"
          :class="dragActive
            ? 'border-primary-400 bg-primary-50/70 dark:border-primary-500 dark:bg-primary-900/20'
            : 'border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800'"
          @dragenter.prevent="handleDragEnter"
          @dragover.prevent
          @dragleave.prevent="handleDragLeave"
          @drop.prevent="handleDrop"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200" :title="fileListTitle">
              {{ selectedFilesLabel || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">
              JSON (.json)
              <span v-if="files.length > 1"> · {{ fileListTitle }}</span>
            </div>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" @click="openFilePicker">
            {{ t('common.chooseFile') }}
          </button>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          multiple
          @change="handleFileChange"
        />
      </div>

      <!-- 预获取范围：第一个 / 全部 -->
      <div
        v-if="files.length && (previewAccountCount > 0 || kiroPreviewNames.length > 0)"
        class="space-y-2"
      >
        <label class="input-label">{{ t('admin.accounts.importScopeLabel') }}</label>
        <div class="grid grid-cols-2 gap-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-800">
          <button
            type="button"
            :class="[
              'rounded-md px-3 py-2 text-sm font-medium transition-colors',
              importScope === 'first'
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-500 dark:text-gray-400'
            ]"
            :disabled="importing"
            @click="importScope = 'first'"
          >
            {{ t('admin.accounts.importScopeFirst') }}
          </button>
          <button
            type="button"
            :class="[
              'rounded-md px-3 py-2 text-sm font-medium transition-colors',
              importScope === 'all'
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-500 dark:text-gray-400'
            ]"
            :disabled="importing"
            @click="importScope = 'all'"
          >
            {{ t('admin.accounts.importScopeAll') }}
          </button>
        </div>
        <p class="text-xs text-gray-500 dark:text-dark-400">
          {{
            importScope === 'first'
              ? t('admin.accounts.importScopeFirstHint', {
                  total: importMode === 'kiro' ? kiroPreviewNames.length : previewAccountCount
                })
              : t('admin.accounts.importScopeAllHint', {
                  total: importMode === 'kiro' ? kiroPreviewNames.length : previewAccountCount
                })
          }}
        </p>
      </div>

      <!-- 按账号类型预编辑 -->
      <div
        v-if="importMode === 'sub2api' && platformEdits.length"
        class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="flex items-center justify-between gap-2">
          <div>
            <div class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.accounts.importPreEditTitle') }}
            </div>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.accounts.importPreEditHint', { count: previewAccountCount }) }}
            </p>
          </div>
        </div>

        <div class="max-h-96 space-y-3 overflow-auto">
          <div
            v-for="row in platformEdits"
            :key="row.platform"
            class="rounded-lg border border-gray-100 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-800"
          >
            <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
              <div class="text-sm font-medium text-gray-800 dark:text-dark-100">
                {{ row.platform }}
                <span class="ml-1 text-xs font-normal text-gray-500 dark:text-dark-400">
                  {{ t('admin.accounts.importPreEditCount', { count: row.count }) }}
                </span>
              </div>
              <div
                v-if="row.sampleNames.length"
                class="max-w-full truncate text-xs text-gray-500 dark:text-dark-400"
                :title="row.sampleNames.join(', ')"
              >
                {{ row.sampleNames.join(', ') }}
              </div>
            </div>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <label class="block space-y-1">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enableConcurrency"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.concurrency') }}
                </span>
                <input
                  v-model.number="row.concurrency"
                  type="number"
                  min="1"
                  max="1000"
                  class="input"
                  :disabled="!row.enableConcurrency"
                  :class="!row.enableConcurrency && 'cursor-not-allowed opacity-50'"
                />
              </label>
              <label class="block space-y-1">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enablePriority"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.priority') }}
                </span>
                <input
                  v-model.number="row.priority"
                  type="number"
                  min="0"
                  max="100"
                  class="input"
                  :disabled="!row.enablePriority"
                  :class="!row.enablePriority && 'cursor-not-allowed opacity-50'"
                />
              </label>
              <label class="block space-y-1">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enableRateMultiplier"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.billingRateMultiplier') }}
                </span>
                <input
                  v-model.number="row.rateMultiplier"
                  type="number"
                  min="0"
                  step="0.01"
                  class="input"
                  :disabled="!row.enableRateMultiplier"
                  :class="!row.enableRateMultiplier && 'cursor-not-allowed opacity-50'"
                />
              </label>
              <label class="block space-y-1">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enableNamePrefix"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.importPreEditNamePrefix') }}
                </span>
                <input
                  v-model="row.namePrefix"
                  type="text"
                  class="input"
                  :disabled="!row.enableNamePrefix"
                  :class="!row.enableNamePrefix && 'cursor-not-allowed opacity-50'"
                  :placeholder="t('admin.accounts.importPreEditNamePrefixPh')"
                />
              </label>
              <label class="block space-y-1">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enableNameSuffix"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.importPreEditNameSuffix') }}
                </span>
                <input
                  v-model="row.nameSuffix"
                  type="text"
                  class="input"
                  :disabled="!row.enableNameSuffix"
                  :class="!row.enableNameSuffix && 'cursor-not-allowed opacity-50'"
                  :placeholder="t('admin.accounts.importPreEditNameSuffixPh')"
                />
              </label>
              <label class="block space-y-1">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enableNotes"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.notes') }}
                </span>
                <input
                  v-model="row.notes"
                  type="text"
                  class="input"
                  :disabled="!row.enableNotes"
                  :class="!row.enableNotes && 'cursor-not-allowed opacity-50'"
                  :placeholder="t('admin.accounts.importPreEditNotesPh')"
                />
              </label>
              <label class="block space-y-1 sm:col-span-3">
                <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
                  <input
                    v-model="row.enableAutoPause"
                    type="checkbox"
                    class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  {{ t('admin.accounts.autoPauseOnExpired') }}
                </span>
                <select
                  v-model="row.autoPauseOnExpired"
                  class="input"
                  :disabled="!row.enableAutoPause"
                  :class="!row.enableAutoPause && 'cursor-not-allowed opacity-50'"
                >
                  <option :value="true">{{ t('common.yes') }}</option>
                  <option :value="false">{{ t('common.no') }}</option>
                </select>
              </label>
            </div>
          </div>
        </div>

        <div
          v-if="previewAccountNames.length"
          class="max-h-28 overflow-auto rounded-lg bg-white p-2 font-mono text-xs text-gray-600 dark:bg-dark-900 dark:text-dark-300"
        >
          <div v-for="(name, idx) in visiblePreviewNames" :key="idx">
            {{ name }}
          </div>
          <div v-if="hiddenPreviewCount > 0" class="text-gray-400">
            {{ t('admin.accounts.importPreEditMore', { count: hiddenPreviewCount }) }}
          </div>
        </div>
      </div>

      <div
        v-else-if="importMode === 'kiro' && kiroPreviewNames.length"
        class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.importPreEditTitle') }}
        </div>
        <p class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.importPreEditHint', { count: kiroPreviewNames.length }) }}
        </p>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label class="block space-y-1">
            <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
              <input
                v-model="enableKiroConcurrency"
                type="checkbox"
                class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              />
              {{ t('admin.accounts.concurrency') }}
            </span>
            <input
              v-model.number="kiroConcurrency"
              type="number"
              min="1"
              max="1000"
              class="input"
              :disabled="!enableKiroConcurrency"
              :class="!enableKiroConcurrency && 'cursor-not-allowed opacity-50'"
            />
          </label>
          <label class="block space-y-1">
            <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
              <input
                v-model="enableKiroPriority"
                type="checkbox"
                class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              />
              {{ t('admin.accounts.priority') }}
            </span>
            <input
              v-model.number="kiroPriority"
              type="number"
              min="0"
              max="100"
              class="input"
              :disabled="!enableKiroPriority"
              :class="!enableKiroPriority && 'cursor-not-allowed opacity-50'"
            />
          </label>
          <label class="block space-y-1">
            <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
              <input
                v-model="enableKiroNamePrefix"
                type="checkbox"
                class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              />
              {{ t('admin.accounts.importPreEditNamePrefix') }}
            </span>
            <input
              v-model="kiroNamePrefix"
              type="text"
              class="input"
              :disabled="!enableKiroNamePrefix"
              :class="!enableKiroNamePrefix && 'cursor-not-allowed opacity-50'"
            />
          </label>
          <label class="block space-y-1">
            <span class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-300">
              <input
                v-model="enableKiroNotes"
                type="checkbox"
                class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              />
              {{ t('admin.accounts.notes') }}
            </span>
            <input
              v-model="kiroNotes"
              type="text"
              class="input"
              :disabled="!enableKiroNotes"
              :class="!enableKiroNotes && 'cursor-not-allowed opacity-50'"
            />
          </label>
        </div>
        <div
          class="max-h-28 overflow-auto rounded-lg bg-gray-50 p-2 font-mono text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300"
        >
          <div v-for="(name, idx) in visibleKiroPreviewNames" :key="idx">
            {{ name || t('admin.accounts.importPreEditUnnamed', { index: idx + 1 }) }}
          </div>
          <div v-if="hiddenKiroPreviewCount > 0" class="text-gray-400">
            {{ t('admin.accounts.importPreEditMore', { count: hiddenKiroPreviewCount }) }}
          </div>
        </div>
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} — {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="import-data-form"
          :disabled="importing"
        >
          {{ importing ? t('admin.accounts.dataImporting') : (importMode === 'kiro' ? t('admin.accounts.kiroImportButton') : t('admin.accounts.dataImportButton')) }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminDataAccount, AdminDataImportResult, AdminDataPayload, AdminGroup, Proxy } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

interface PlatformPreEdit {
  platform: string
  count: number
  sampleNames: string[]
  enableConcurrency: boolean
  concurrency: number
  enablePriority: boolean
  priority: number
  enableRateMultiplier: boolean
  rateMultiplier: number
  enableNamePrefix: boolean
  namePrefix: string
  enableNameSuffix: boolean
  nameSuffix: string
  enableNotes: boolean
  notes: string
  enableAutoPause: boolean
  autoPauseOnExpired: boolean
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const importMode = ref<'sub2api' | 'kiro'>('sub2api')
const importScope = ref<'first' | 'all'>('all')
const selectedGroupIds = ref<number[]>([])
const selectedProxyId = ref<number | null>(null)
const groups = ref<AdminGroup[]>([])
const proxies = ref<Proxy[]>([])
const files = ref<File[]>([])
const dragDepth = ref(0)
const dragActive = computed(() => dragDepth.value > 0)
const hasCreatedData = ref(false)
const result = ref<AdminDataImportResult | null>(null)

const platformEdits = ref<PlatformPreEdit[]>([])
const previewAccountCount = ref(0)
const previewAccountNames = ref<string[]>([])
const kiroPreviewNames = ref<string[]>([])
const enableKiroConcurrency = ref(false)
const kiroConcurrency = ref(3)
const enableKiroPriority = ref(false)
const kiroPriority = ref(50)
const enableKiroNamePrefix = ref(false)
const kiroNamePrefix = ref('')
const enableKiroNotes = ref(false)
const kiroNotes = ref('')

const fileInput = ref<HTMLInputElement | null>(null)
const selectedFilesLabel = computed(() => {
  if (files.value.length === 0) return ''
  if (files.value.length === 1) return files.value[0]?.name || ''
  return t('admin.accounts.selectedCount', { count: files.value.length })
})
const fileListTitle = computed(() => files.value.map((item) => item.name).join(', '))

const errorItems = computed(() => result.value?.errors || [])

const scopedPreviewNames = (names: string[]) => {
  if (importScope.value === 'first') return names.slice(0, 1)
  return names
}

const visiblePreviewNames = computed(() => scopedPreviewNames(previewAccountNames.value).slice(0, 50))
const hiddenPreviewCount = computed(() => {
  const scoped = scopedPreviewNames(previewAccountNames.value)
  return Math.max(0, scoped.length - 50)
})
const visibleKiroPreviewNames = computed(() => scopedPreviewNames(kiroPreviewNames.value).slice(0, 50))
const hiddenKiroPreviewCount = computed(() => {
  const scoped = scopedPreviewNames(kiroPreviewNames.value)
  return Math.max(0, scoped.length - 50)
})

const resetPreview = () => {
  platformEdits.value = []
  previewAccountCount.value = 0
  previewAccountNames.value = []
  kiroPreviewNames.value = []
  enableKiroConcurrency.value = false
  kiroConcurrency.value = 3
  enableKiroPriority.value = false
  kiroPriority.value = 50
  enableKiroNamePrefix.value = false
  kiroNamePrefix.value = ''
  enableKiroNotes.value = false
  kiroNotes.value = ''
  importScope.value = 'all'
}

watch(
  () => props.show,
  async (open) => {
    if (open) {
      files.value = []
      dragDepth.value = 0
      hasCreatedData.value = false
      result.value = null
      selectedGroupIds.value = []
      selectedProxyId.value = null
      resetPreview()
      if (fileInput.value) {
        fileInput.value.value = ''
      }
      try {
        if (groups.value.length === 0) {
          groups.value = await adminAPI.groups.getAllIncludingInactive()
        }
      } catch {
        groups.value = []
      }
      try {
        if (proxies.value.length === 0) {
          proxies.value = await adminAPI.proxies.getAllWithCount()
        }
      } catch {
        proxies.value = []
      }
    }
  }
)

const setImportMode = (mode: 'sub2api' | 'kiro') => {
  if (importing.value || importMode.value === mode) return
  importMode.value = mode
  files.value = []
  result.value = null
  resetPreview()
  if (fileInput.value) fileInput.value.value = ''
}

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  void setSelectedFiles(target.files)
  target.value = ''
}

const handleClose = () => {
  if (importing.value) return
  if (hasCreatedData.value) {
    hasCreatedData.value = false
    emit('imported')
  }
  emit('close')
}

const isJsonFile = (sourceFile: File) => {
  const name = sourceFile.name.toLowerCase()
  return name.endsWith('.json') || sourceFile.type === 'application/json'
}

const setSelectedFiles = async (sourceFiles: FileList | File[] | null | undefined) => {
  if (importing.value) return
  const incoming = Array.from(sourceFiles || [])
  const picked = incoming.filter(isJsonFile)
  if (!picked.length) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }
  if (picked.length < incoming.length) {
    appStore.showWarning(
      t('admin.accounts.dataImportIgnoredFiles', { count: incoming.length - picked.length })
    )
  }
  files.value = picked
  result.value = null
  importScope.value = 'all'
  enableKiroConcurrency.value = false
  kiroConcurrency.value = 3
  enableKiroPriority.value = false
  kiroPriority.value = 50
  enableKiroNamePrefix.value = false
  kiroNamePrefix.value = ''
  enableKiroNotes.value = false
  kiroNotes.value = ''
  await rebuildPreview()
}

const handleDragEnter = () => {
  if (importing.value) return
  dragDepth.value += 1
}

const handleDragLeave = () => {
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

const handleDrop = (event: DragEvent) => {
  dragDepth.value = 0
  if (importing.value) return
  void setSelectedFiles(event.dataTransfer?.files)
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') {
    return sourceFile.text()
  }

  if (typeof sourceFile.arrayBuffer === 'function') {
    const buffer = await sourceFile.arrayBuffer()
    return new TextDecoder().decode(buffer)
  }

  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(sourceFile)
  })
}

const SUPPORTED_DATA_TYPES = ['sub2api-data', 'sub2api-bundle']
const SUPPORTED_DATA_VERSION = 1

// 与后端 validateDataHeader 对齐:合并前逐文件校验,避免坏文件混入合并 payload 后
// 报错无法定位来源,或绕过后端本会对单文件做的 type/version 检查。
const isValidDataPayload = (payload: unknown): payload is AdminDataPayload => {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return false
  const candidate = payload as Record<string, unknown>
  if (
    candidate.type !== undefined &&
    candidate.type !== '' &&
    !SUPPORTED_DATA_TYPES.includes(candidate.type as string)
  ) {
    return false
  }
  if (
    candidate.version !== undefined &&
    candidate.version !== 0 &&
    candidate.version !== SUPPORTED_DATA_VERSION
  ) {
    return false
  }
  return Array.isArray(candidate.proxies) && Array.isArray(candidate.accounts)
}

const extractKiroAccounts = (payload: unknown): unknown[] => {
  if (Array.isArray(payload)) return payload
  if (!payload || typeof payload !== 'object') return []
  const record = payload as Record<string, unknown>
  if (Array.isArray(record.accounts)) return record.accounts
  return [payload]
}

const kiroAccountDisplayName = (item: unknown, index: number): string => {
  if (!item || typeof item !== 'object') {
    return t('admin.accounts.importPreEditUnnamed', { index: index + 1 })
  }
  const record = item as Record<string, unknown>
  const credentials =
    record.credentials && typeof record.credentials === 'object'
      ? (record.credentials as Record<string, unknown>)
      : null
  const name =
    (typeof record.name === 'string' && record.name) ||
    (typeof record.displayName === 'string' && record.displayName) ||
    (typeof record.email === 'string' && record.email) ||
    (credentials && typeof credentials.email === 'string' && credentials.email) ||
    ''
  return name || t('admin.accounts.importPreEditUnnamed', { index: index + 1 })
}

const mergeDataPayloads = (payloads: AdminDataPayload[]): AdminDataPayload => {
  const [firstPayload] = payloads
  if (payloads.length === 1 && firstPayload) return firstPayload

  return {
    type: payloads.find((item) => typeof item.type === 'string')?.type,
    version: payloads.find((item) => typeof item.version === 'number')?.version,
    exported_at: new Date().toISOString(),
    proxies: payloads.flatMap((item) => item.proxies),
    accounts: payloads.flatMap((item) => item.accounts),
    skipped_shadows: payloads.reduce((sum, item) => {
      const count = Number(item.skipped_shadows || 0)
      return Number.isFinite(count) ? sum + count : sum
    }, 0)
  }
}

const buildPlatformEdits = (accounts: AdminDataAccount[]): PlatformPreEdit[] => {
  const groupsMap = new Map<string, AdminDataAccount[]>()
  for (const account of accounts) {
    const platform = (account.platform || 'unknown').trim() || 'unknown'
    const list = groupsMap.get(platform) || []
    list.push(account)
    groupsMap.set(platform, list)
  }

  return Array.from(groupsMap.entries())
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([platform, list]) => {
      const first = list[0]
      return {
        platform,
        count: list.length,
        sampleNames: list
          .map((item) => item.name)
          .filter(Boolean)
          .slice(0, 3),
        enableConcurrency: false,
        concurrency: Number(first?.concurrency) > 0 ? Number(first.concurrency) : 3,
        enablePriority: false,
        priority: Number.isFinite(Number(first?.priority)) ? Number(first.priority) : 50,
        enableRateMultiplier: false,
        rateMultiplier:
          first?.rate_multiplier != null && Number.isFinite(Number(first.rate_multiplier))
            ? Number(first.rate_multiplier)
            : 1,
        enableNamePrefix: false,
        namePrefix: '',
        enableNameSuffix: false,
        nameSuffix: '',
        enableNotes: false,
        notes: first?.notes || '',
        enableAutoPause: false,
        autoPauseOnExpired: first?.auto_pause_on_expired !== false
      }
    })
}

const applyNameTransform = (name: string, prefix: string, suffix: string) => {
  const base = (name || '').trim() || 'imported-account'
  return `${prefix}${base}${suffix}`
}

const applyPlatformEdits = (accounts: AdminDataAccount[]): AdminDataAccount[] => {
  if (!platformEdits.value.length) return accounts
  const editByPlatform = new Map(platformEdits.value.map((row) => [row.platform, row]))
  return accounts.map((account) => {
    const platform = (account.platform || 'unknown').trim() || 'unknown'
    const edit = editByPlatform.get(platform)
    if (!edit) return account
    const next: AdminDataAccount = { ...account }
    if (edit.enableConcurrency) {
      next.concurrency = Math.max(1, Math.floor(Number(edit.concurrency) || 1))
    }
    if (edit.enablePriority) {
      const value = Math.floor(Number(edit.priority))
      next.priority = Number.isFinite(value) ? value : 50
    }
    if (edit.enableRateMultiplier) {
      const value = Number(edit.rateMultiplier)
      next.rate_multiplier = Number.isFinite(value) ? value : 1
    }
    if (edit.enableNamePrefix || edit.enableNameSuffix) {
      next.name = applyNameTransform(
        account.name,
        edit.enableNamePrefix ? edit.namePrefix : '',
        edit.enableNameSuffix ? edit.nameSuffix : ''
      )
    }
    if (edit.enableNotes) {
      next.notes = edit.notes
    }
    if (edit.enableAutoPause) {
      next.auto_pause_on_expired = !!edit.autoPauseOnExpired
    }
    return next
  })
}

const limitAccountsByScope = <T,>(accounts: T[]): T[] => {
  if (importScope.value === 'first') return accounts.slice(0, 1)
  return accounts
}

const applyKiroPreEdits = (accounts: unknown[]): unknown[] => {
  if (!enableKiroNamePrefix.value && !enableKiroNotes.value) return accounts
  return accounts.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      if (!enableKiroNamePrefix.value) return item
      return {
        name: applyNameTransform(
          kiroAccountDisplayName(item, index),
          kiroNamePrefix.value,
          ''
        )
      }
    }
    const record = { ...(item as Record<string, unknown>) }
    if (enableKiroNamePrefix.value) {
      const current =
        (typeof record.name === 'string' && record.name) ||
        (typeof record.displayName === 'string' && record.displayName) ||
        (typeof record.email === 'string' && record.email) ||
        kiroAccountDisplayName(item, index)
      record.name = applyNameTransform(String(current), kiroNamePrefix.value, '')
    }
    if (enableKiroNotes.value) {
      record.notes = kiroNotes.value
    }
    return record
  })
}

const parseSelectedFiles = async (): Promise<unknown[] | null> => {
  if (files.value.length === 0) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return null
  }
  const parsedFiles: unknown[] = []
  for (const sourceFile of files.value) {
    try {
      parsedFiles.push(JSON.parse(await readFileAsText(sourceFile)))
    } catch {
      appStore.showError(
        t('admin.accounts.dataImportParseFailedFile', { name: sourceFile.name })
      )
      return null
    }
  }
  return parsedFiles
}

const rebuildPreview = async () => {
  // keep importScope across rebuild? reset only lists; default all on new files via setSelectedFiles
  platformEdits.value = []
  previewAccountCount.value = 0
  previewAccountNames.value = []
  kiroPreviewNames.value = []
  if (files.value.length === 0) return

  const parsedFiles: unknown[] = []
  for (const sourceFile of files.value) {
    try {
      parsedFiles.push(JSON.parse(await readFileAsText(sourceFile)))
    } catch {
      // 预览失败不打断选文件；提交时会再次校验并提示
      return
    }
  }

  if (importMode.value === 'kiro') {
    const accounts = parsedFiles.flatMap(extractKiroAccounts)
    kiroPreviewNames.value = accounts.map((item, index) => kiroAccountDisplayName(item, index))
    return
  }

  const dataPayloads: AdminDataPayload[] = []
  for (const parsed of parsedFiles) {
    if (!isValidDataPayload(parsed)) {
      return
    }
    dataPayloads.push(parsed)
  }
  const dataPayload = mergeDataPayloads(dataPayloads)
  previewAccountCount.value = dataPayload.accounts.length
  previewAccountNames.value = dataPayload.accounts.map(
    (item, index) => item.name || t('admin.accounts.importPreEditUnnamed', { index: index + 1 })
  )
  platformEdits.value = buildPlatformEdits(dataPayload.accounts)
}

const handleImport = async () => {
  const parsedFiles = await parseSelectedFiles()
  if (!parsedFiles) return

  importing.value = true
  try {
    if (importMode.value === 'kiro') {
      let accounts = parsedFiles.flatMap(extractKiroAccounts)
      accounts = limitAccountsByScope(accounts)
      accounts = applyKiroPreEdits(accounts)
      const kiroPayload = accounts.length === 1 ? accounts[0] : { accounts }
      const kiroResult = await adminAPI.accounts.importKiroCredentials(kiroPayload, {
        group_ids: selectedGroupIds.value.length ? selectedGroupIds.value : undefined,
        proxy_id: selectedProxyId.value,
        concurrency: enableKiroConcurrency.value ? kiroConcurrency.value : undefined,
        priority: enableKiroPriority.value ? kiroPriority.value : undefined,
        notes: enableKiroNotes.value ? kiroNotes.value : undefined
      })
      if (kiroResult.created > 0) emit('imported')
      const message = t('admin.accounts.kiroImportSuccess', {
        created: kiroResult.created,
        failed: kiroResult.failed
      })
      if (kiroResult.failed > 0) appStore.showWarning(message)
      else appStore.showSuccess(message)
      return
    }

    const dataPayloads: AdminDataPayload[] = []
    for (let index = 0; index < parsedFiles.length; index++) {
      const parsed = parsedFiles[index]
      if (!isValidDataPayload(parsed)) {
        appStore.showError(t('admin.accounts.dataImportInvalidFile', { name: files.value[index]?.name || '-' }))
        return
      }
      dataPayloads.push(parsed)
    }
    const dataPayload = mergeDataPayloads(dataPayloads)
    dataPayload.accounts = limitAccountsByScope(dataPayload.accounts)
    dataPayload.accounts = applyPlatformEdits(dataPayload.accounts)

    const res = await adminAPI.accounts.importData({
      data: dataPayload,
      skip_default_group_bind: true,
      group_ids: selectedGroupIds.value.length ? selectedGroupIds.value : undefined,
      proxy_id: selectedProxyId.value
    })

    result.value = res

    const msgParams: Record<string, unknown> = {
      account_created: res.account_created,
      account_failed: res.account_failed,
      proxy_created: res.proxy_created,
      proxy_reused: res.proxy_reused,
      proxy_failed: res.proxy_failed,
    }
    if (res.account_failed > 0 || res.proxy_failed > 0) {
      // 部分成功也创建了数据;弹窗关闭时通过 imported 通知父组件刷新列表
      if (res.account_created > 0 || res.proxy_created > 0) {
        hasCreatedData.value = true
      }
      appStore.showError(t('admin.accounts.dataImportCompletedWithErrors', msgParams))
    } else {
      appStore.showSuccess(t('admin.accounts.dataImportSuccess', msgParams))
      emit('imported')
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
  } finally {
    importing.value = false
  }
}
</script>
