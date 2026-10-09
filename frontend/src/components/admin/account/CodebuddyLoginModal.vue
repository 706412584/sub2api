<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.codebuddyLoginTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="codebuddy-login-form" class="space-y-4" @submit.prevent="handleCreate">
      <div class="rounded-lg border border-sky-200 bg-sky-50 px-3 py-2 text-sm text-sky-800 dark:border-sky-800/50 dark:bg-sky-900/20 dark:text-sky-200">
        {{ t('admin.accounts.codebuddyLoginDesc') }}
      </div>

      <div>
        <label class="input-label">{{ t('common.name') }}</label>
        <input
          v-model="name"
          type="text"
          class="input"
          :placeholder="t('admin.accounts.codebuddyLoginNamePlaceholder')"
          :disabled="busy"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.codebuddyLoginRegion') }}</label>
        <div class="grid grid-cols-2 gap-3">
          <button
            type="button"
            :class="[
              'flex flex-col items-start rounded-lg border-2 p-3 text-left transition-all',
              region === 'cn'
                ? 'border-sky-300 bg-sky-50 dark:border-sky-700/60 dark:bg-sky-900/20'
                : 'border-gray-200 hover:border-sky-300 dark:border-dark-600 dark:hover:border-sky-700'
            ]"
            :disabled="busy || polling"
            @click="region = 'cn'"
          >
            <span class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.codebuddyRegionCN') }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codebuddyRegionCNDesc') }}</span>
          </button>
          <button
            type="button"
            :class="[
              'flex flex-col items-start rounded-lg border-2 p-3 text-left transition-all',
              region === 'global'
                ? 'border-sky-300 bg-sky-50 dark:border-sky-700/60 dark:bg-sky-900/20'
                : 'border-gray-200 hover:border-sky-300 dark:border-dark-600 dark:hover:border-sky-700'
            ]"
            :disabled="busy || polling"
            @click="region = 'global'"
          >
            <span class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.codebuddyRegionGlobal') }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.codebuddyRegionGlobalDesc') }}</span>
          </button>
        </div>
      </div>

      <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.codebuddyLoginSectionTitle') }}</div>
            <p class="mt-1 text-sm text-gray-600 dark:text-dark-300">
              {{ t('admin.accounts.codebuddyLoginSectionDesc') }}
            </p>
          </div>
          <button class="btn btn-primary" type="button" :disabled="busy" @click="handleStart">
            {{ session ? t('admin.accounts.codebuddyLoginRestartButton') : t('admin.accounts.codebuddyLoginStartButton') }}
          </button>
        </div>

        <div v-if="session" class="mt-4 space-y-3">
          <div class="grid gap-3 sm:grid-cols-2">
            <div>
              <div class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
                {{ t('admin.accounts.codebuddyLoginStatus') }}
              </div>
              <div class="mt-1 text-sm text-gray-700 dark:text-dark-300">
                {{ statusText }}
              </div>
            </div>
            <div v-if="isAuthorized && authorizedNickname">
              <div class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
                {{ t('admin.accounts.codebuddyLoginAccount') }}
              </div>
              <div class="mt-1 text-sm text-gray-700 dark:text-dark-300">
                {{ authorizedNickname }}
              </div>
            </div>
          </div>

          <div class="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-700 dark:border-dark-700 dark:bg-dark-900/40 dark:text-dark-200 break-all">
            {{ session.auth_url }}
          </div>

          <div class="flex flex-wrap gap-3">
            <button class="btn btn-secondary" type="button" :disabled="busy" @click="openAuthPage">
              {{ t('admin.accounts.codebuddyLoginOpenPageButton') }}
            </button>
            <button
              class="btn btn-secondary"
              type="button"
              :disabled="busy || !session || isAuthorized"
              @click="() => handlePollOnce()"
            >
              {{ polling ? t('admin.accounts.codebuddyLoginPolling') : t('admin.accounts.codebuddyLoginCheckStatusButton') }}
            </button>
          </div>

          <p class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.accounts.codebuddyLoginBrowserHint') }}
          </p>
        </div>
      </div>

      <details class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
        <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.codebuddyImportTitle') }}
        </summary>
        <div class="mt-3 space-y-3">
          <div>
            <label class="input-label">{{ t('admin.accounts.codebuddyImportPayload') }}</label>
            <textarea
              v-model="payloadText"
              rows="8"
              class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-gray-800 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-100"
              :placeholder="t('admin.accounts.codebuddyImportPayloadPlaceholder')"
              spellcheck="false"
              :disabled="busy"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.accounts.codebuddyImportPayloadHint') }}
            </p>
          </div>
          <button class="btn btn-secondary" type="button" :disabled="busy" @click="handleImport">
            {{ t('admin.accounts.codebuddyImportButton') }}
          </button>
        </div>
      </details>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="busy" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button class="btn btn-primary" type="submit" form="codebuddy-login-form" :disabled="busy || !isAuthorized">
          {{ creating ? t('common.saving') : t('admin.accounts.codebuddyLoginCreateButton') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type {
  CodebuddyLoginStartResponse,
  CodebuddyOAuthCreateAccountRequest
} from '@/api/admin/accounts'

type CodebuddyAccountDefaults = Omit<CodebuddyOAuthCreateAccountRequest, 'session_id'>

interface Props {
  show: boolean
  accountDefaults?: CodebuddyAccountDefaults
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
const starting = ref(false)
const polling = ref(false)
const name = ref('')
const region = ref<'cn' | 'global'>('cn')
const payloadText = ref('')
const session = ref<CodebuddyLoginStartResponse | null>(null)
const authorized = ref(false)
const authorizedNickname = ref('')
const pollTimer = ref<number | null>(null)

const busy = computed(() => creating.value || starting.value)
const isAuthorized = computed(() => authorized.value)
const statusText = computed(() => {
  if (isAuthorized.value) return t('admin.accounts.codebuddyLoginAuthorized')
  if (polling.value) return t('admin.accounts.codebuddyLoginPollingStatus')
  if (session.value) return t('admin.accounts.codebuddyLoginPendingStatus')
  return t('admin.accounts.codebuddyLoginNotStartedStatus')
})

watch(
  () => props.show,
  (show) => {
    if (show) {
      resetState()
    } else {
      stopPolling()
    }
  }
)

onBeforeUnmount(() => {
  stopPolling()
})

const resetState = () => {
  stopPolling()
  creating.value = false
  starting.value = false
  polling.value = false
  name.value = props.accountDefaults?.name || ''
  payloadText.value = ''
  session.value = null
  authorized.value = false
  authorizedNickname.value = ''
}

const stopPolling = () => {
  if (pollTimer.value != null) {
    window.clearTimeout(pollTimer.value)
    pollTimer.value = null
  }
  polling.value = false
}

const scheduleNextPoll = () => {
  stopPolling()
  if (!session.value || isAuthorized.value) return
  pollTimer.value = window.setTimeout(() => {
    handlePollOnce(true)
  }, 5000)
}

const handleClose = () => {
  if (busy.value || polling.value) return
  emit('close')
}

const openAuthPage = () => {
  if (!session.value) return
  window.open(session.value.auth_url, '_blank', 'noopener,noreferrer')
}

const handleStart = async () => {
  starting.value = true
  stopPolling()
  authorized.value = false
  authorizedNickname.value = ''
  try {
    session.value = await adminAPI.accounts.startCodebuddyLogin({ region: region.value })
    if (session.value.region === 'global') region.value = 'global'
    appStore.showSuccess(t('admin.accounts.codebuddyLoginStartSuccess'))
    openAuthPage()
    scheduleNextPoll()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.codebuddyLoginStartFailed'))
  } finally {
    starting.value = false
  }
}

const handlePollOnce = async (silent = false) => {
  if (!session.value) return
  polling.value = true
  try {
    const result = await adminAPI.accounts.pollCodebuddyLogin({
      session_id: session.value.session_id
    })
    if (result.status === 'authorized') {
      authorized.value = true
      authorizedNickname.value = result.nickname || ''
      stopPolling()
      if (!silent) {
        appStore.showSuccess(t('admin.accounts.codebuddyLoginAuthorizedSuccess'))
      }
      return
    }
    scheduleNextPoll()
  } catch (error: any) {
    stopPolling()
    appStore.showError(error?.message || t('admin.accounts.codebuddyLoginPollFailed'))
  } finally {
    if (!pollTimer.value) {
      polling.value = false
    }
  }
}

const handleCreate = async () => {
  if (!session.value || !isAuthorized.value) {
    appStore.showError(t('admin.accounts.codebuddyLoginAuthorizeFirst'))
    return
  }
  creating.value = true
  try {
    await adminAPI.accounts.createCodebuddyOAuthAccount({
      ...props.accountDefaults,
      name: name.value.trim() || props.accountDefaults?.name || undefined,
      session_id: session.value.session_id
    })
    appStore.showSuccess(t('admin.accounts.codebuddyLoginCreateSuccess'))
    emit('created')
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.codebuddyLoginCreateFailed'))
  } finally {
    creating.value = false
  }
}

const handleImport = async () => {
  const trimmed = payloadText.value.trim()
  if (!trimmed) {
    appStore.showError(t('admin.accounts.codebuddyImportPayloadRequired'))
    return
  }
  let data: unknown
  try {
    data = JSON.parse(trimmed)
  } catch {
    appStore.showError(t('admin.accounts.dataImportParseFailed'))
    return
  }
  creating.value = true
  try {
    const result = await adminAPI.accounts.importCodebuddyAuths(data, {
      region: region.value,
      name: name.value.trim() || undefined,
      group_ids: props.accountDefaults?.group_ids,
      proxy_id: props.accountDefaults?.proxy_id,
      concurrency: props.accountDefaults?.concurrency,
      priority: props.accountDefaults?.priority
    })
    if (result.created > 0) {
      appStore.showSuccess(
        t('admin.accounts.codebuddyImportSuccess', { created: result.created, failed: result.failed })
      )
      emit('created')
    } else {
      const firstError = result.errors?.[0]?.message || result.items?.find((i) => i.action === 'failed')?.message
      appStore.showError(firstError || t('admin.accounts.codebuddyImportFailed'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.codebuddyImportFailed'))
  } finally {
    creating.value = false
  }
}
</script>
