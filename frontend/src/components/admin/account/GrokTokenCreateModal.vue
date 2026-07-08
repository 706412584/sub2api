<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.grokTokenTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="grok-token-create-form" class="space-y-4" @submit.prevent="handleCreate">
      <div class="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900/20 dark:text-slate-200">
        {{ t('admin.accounts.grokTokenDesc') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.name') }}</label>
        <input
          v-model="name"
          type="text"
          class="input"
          :placeholder="t('admin.accounts.grokTokenNamePlaceholder')"
          :disabled="creating"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.grokTokenPayload') }}</label>
        <textarea
          v-model="payloadText"
          rows="10"
          class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-gray-800 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-100"
          :placeholder="t('admin.accounts.grokTokenPayloadPlaceholder')"
          spellcheck="false"
          :disabled="creating"
        />
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.grokTokenPayloadHint') }}
        </p>
      </div>

      <div v-if="preview" class="rounded-xl border border-gray-200 p-3 text-sm dark:border-dark-700">
        <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.grokTokenPreview') }}</div>
        <div class="mt-2 space-y-1 text-gray-700 dark:text-dark-300">
          <div>{{ t('admin.accounts.name') }}: {{ preview.name }}</div>
          <div>{{ t('admin.accounts.type') }}: grok / oauth</div>
          <div v-if="preview.extra?.email">Email: {{ preview.extra.email }}</div>
          <div v-if="preview.extra?.username">Username: {{ preview.extra.username }}</div>
          <div v-if="preview.extra?.display_name">Display: {{ preview.extra.display_name }}</div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-between gap-3">
        <button class="btn btn-secondary" type="button" :disabled="creating" @click="handlePreview">
          {{ t('admin.accounts.grokTokenPreviewButton') }}
        </button>
        <div class="flex gap-3">
          <button class="btn btn-secondary" type="button" :disabled="creating" @click="handleClose">
            {{ t('common.cancel') }}
          </button>
          <button class="btn btn-primary" type="submit" form="grok-token-create-form" :disabled="creating">
            {{ creating ? t('common.saving') : t('admin.accounts.grokTokenCreateButton') }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { GrokOAuthNormalizeResponse } from '@/api/admin/accounts'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'created'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const creating = ref(false)
const name = ref('')
const payloadText = ref('')
const preview = ref<GrokOAuthNormalizeResponse | null>(null)

watch(
  () => props.show,
  (show) => {
    if (show) {
      name.value = ''
      payloadText.value = ''
      preview.value = null
      creating.value = false
    }
  }
)

const parsePayload = () => {
  const trimmed = payloadText.value.trim()
  if (!trimmed) {
    throw new Error(t('admin.accounts.grokTokenPayloadRequired'))
  }
  return JSON.parse(trimmed)
}

const handleClose = () => {
  if (creating.value) return
  emit('close')
}

const handlePreview = async () => {
  try {
    const data = parsePayload()
    preview.value = await adminAPI.accounts.normalizeGrokOAuthCredentials({ data })
  } catch (error: any) {
    const message = error instanceof SyntaxError
      ? t('admin.accounts.dataImportParseFailed')
      : error?.message || t('admin.accounts.grokTokenPreviewFailed')
    appStore.showError(message)
  }
}

const handleCreate = async () => {
  creating.value = true
  try {
    const data = parsePayload()
    await adminAPI.accounts.createGrokOAuthAccount({
      data,
      name: name.value.trim() || undefined,
      skip_default_group_bind: true
    })
    appStore.showSuccess(t('admin.accounts.grokTokenCreateSuccess'))
    emit('created')
  } catch (error: any) {
    const message = error instanceof SyntaxError
      ? t('admin.accounts.dataImportParseFailed')
      : error?.message || t('admin.accounts.grokTokenCreateFailed')
    appStore.showError(message)
  } finally {
    creating.value = false
  }
}
</script>
