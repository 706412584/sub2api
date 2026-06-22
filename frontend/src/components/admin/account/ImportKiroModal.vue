<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.kiroImportTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="import-kiro-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.kiroImportDesc') }}
      </div>
      <div class="text-xs text-gray-500 dark:text-dark-400">
        {{ t('admin.accounts.kiroImportSourceHint') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex items-center justify-between gap-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200">
              {{ selectedFileLabel || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">JSON (.json)</div>
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

      <div>
        <label class="input-label">{{ t('admin.accounts.kiroImportPasteLabel') }}</label>
        <textarea
          v-model="pastedText"
          rows="6"
          class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-gray-800 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-100"
          :placeholder="t('admin.accounts.kiroImportPastePlaceholder')"
          spellcheck="false"
        />
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.kiroImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.kiroImportResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.name || '-' }} — {{ item.message }}
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
          form="import-kiro-form"
          :disabled="importing"
        >
          {{ importing ? t('admin.accounts.dataImporting') : t('admin.accounts.kiroImportButton') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { KiroImportResult } from '@/api/admin/accounts'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const files = ref<File[]>([])
const pastedText = ref('')
const result = ref<KiroImportResult | null>(null)

const fileInput = ref<HTMLInputElement | null>(null)
const selectedFileLabel = computed(() => {
  if (files.value.length === 0) return ''
  if (files.value.length === 1) return files.value[0].name
  return t('admin.accounts.dataImportSelectedFiles', { count: files.value.length })
})

const errorItems = computed(() => result.value?.errors || [])

watch(
  () => props.show,
  (open) => {
    if (open) {
      files.value = []
      pastedText.value = ''
      result.value = null
      if (fileInput.value) {
        fileInput.value.value = ''
      }
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  files.value = Array.from(target.files || [])
}

const handleClose = () => {
  if (importing.value) return
  emit('close')
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

const handleImport = async () => {
  const trimmedPaste = pastedText.value.trim()
  if (files.value.length === 0 && !trimmedPaste) {
    appStore.showError(t('admin.accounts.kiroImportPasteEmpty'))
    return
  }

  importing.value = true
  try {
    const totalResult: KiroImportResult = {
      total: 0,
      created: 0,
      failed: 0,
      errors: []
    }

    type ImportSource = { label: string; getText: () => Promise<string> }
    const sources: ImportSource[] = files.value.map((file) => ({
      label: file.name,
      getText: () => readFileAsText(file)
    }))
    if (trimmedPaste) {
      sources.push({
        label: t('admin.accounts.kiroImportPasteLabel'),
        getText: async () => trimmedPaste
      })
    }

    for (const source of sources) {
      try {
        const text = await source.getText()
        const dataPayload = JSON.parse(text)

        const res = await adminAPI.accounts.importKiroAccounts({
          data: dataPayload,
          skip_default_group_bind: true
        })

        totalResult.total += res.total
        totalResult.created += res.created
        totalResult.failed += res.failed
        totalResult.errors?.push(
          ...(res.errors || []).map((item: { index: number; name?: string; message: string }) => ({
            ...item,
            message: `[${source.label}] ${item.message}`
          }))
        )
      } catch (error: any) {
        const message = error instanceof SyntaxError
          ? t('admin.accounts.dataImportParseFailed')
          : error?.message || t('admin.accounts.kiroImportFailed')
        totalResult.total += 1
        totalResult.failed += 1
        totalResult.errors?.push({
          index: -1,
          name: source.label,
          message: `[${source.label}] ${message}`
        })
      }
    }

    result.value = totalResult

    const msgParams: Record<string, unknown> = {
      total: totalResult.total,
      created: totalResult.created,
      failed: totalResult.failed,
    }
    if (totalResult.failed > 0) {
      appStore.showError(t('admin.accounts.kiroImportSuccess', msgParams))
    } else {
      appStore.showSuccess(t('admin.accounts.kiroImportSuccess', msgParams))
      emit('imported')
    }
  } finally {
    importing.value = false
  }
}
</script>
