<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.kiroLoginTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="kiro-login-form" class="space-y-4" @submit.prevent="handleCreateFromDeviceFlow">
      <div class="rounded-lg border border-blue-200 bg-blue-50 px-3 py-2 text-sm text-blue-800 dark:border-blue-800/50 dark:bg-blue-900/20 dark:text-blue-200">
        {{ t('admin.accounts.kiroLoginDesc') }}
      </div>

      <div>
        <label class="input-label">{{ t('common.name') }}</label>
        <input
          v-model="name"
          type="text"
          class="input"
          :placeholder="t('admin.accounts.kiroLoginNamePlaceholder')"
          :disabled="busy"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.kiroLoginRegion') }}</label>
        <input
          v-model="region"
          type="text"
          class="input"
          placeholder="us-east-1"
          :disabled="busy || polling"
        />
      </div>

      <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.kiroLoginBrowserSectionTitle') }}</div>
            <p class="mt-1 text-sm text-gray-600 dark:text-dark-300">
              {{ t('admin.accounts.kiroLoginBrowserSectionDesc') }}
            </p>
          </div>
          <button class="btn btn-primary" type="button" :disabled="busy" @click="handleStart">
            {{ session ? t('admin.accounts.kiroLoginRestartButton') : t('admin.accounts.kiroLoginStartButton') }}
          </button>
        </div>

        <div v-if="session" class="mt-4 space-y-3">
          <div class="grid gap-3 sm:grid-cols-2">
            <div>
              <div class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
                {{ t('admin.accounts.kiroLoginUserCode') }}
              </div>
              <div class="mt-1 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-base text-gray-900 dark:border-dark-700 dark:bg-dark-900/40 dark:text-white">
                {{ session.user_code }}
              </div>
            </div>
            <div>
              <div class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
                {{ t('admin.accounts.kiroLoginStatus') }}
              </div>
              <div class="mt-1 text-sm text-gray-700 dark:text-dark-300">
                {{ statusText }}
              </div>
            </div>
          </div>

          <div class="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-700 dark:border-dark-700 dark:bg-dark-900/40 dark:text-dark-200 break-all">
            {{ verificationLink }}
          </div>

          <div class="flex flex-wrap gap-3">
            <button class="btn btn-secondary" type="button" :disabled="busy" @click="openVerificationPage">
              {{ t('admin.accounts.kiroLoginOpenPageButton') }}
            </button>
            <button
              class="btn btn-secondary"
              type="button"
              :disabled="busy || !session || isAuthorized"
              @click="() => handlePollOnce()"
            >
              {{ polling ? t('admin.accounts.kiroLoginPolling') : t('admin.accounts.kiroLoginCheckStatusButton') }}
            </button>
          </div>

          <p class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.accounts.kiroLoginBrowserHint') }}
          </p>
        </div>
      </div>

      <details class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
        <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.kiroLoginAdvancedImportTitle') }}
        </summary>
        <div class="mt-3 space-y-3">
          <div>
            <label class="input-label">{{ t('admin.accounts.kiroLoginPayload') }}</label>
            <textarea
              v-model="payloadText"
              rows="8"
              class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-gray-800 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-100"
              :placeholder="t('admin.accounts.kiroLoginPayloadPlaceholder')"
              spellcheck="false"
              :disabled="busy"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.accounts.kiroLoginPayloadHint') }}
            </p>
          </div>

          <div v-if="preview" class="rounded-xl border border-gray-200 p-3 text-sm dark:border-dark-700">
            <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.kiroLoginPreview') }}</div>
            <div class="mt-2 space-y-1 text-gray-700 dark:text-dark-300">
              <div>{{ t('common.name') }}: {{ preview.name }}</div>
              <div>{{ t('admin.accounts.accountType') }}: kiro / oauth</div>
            </div>
          </div>

          <div class="flex flex-wrap gap-3">
            <button class="btn btn-secondary" type="button" :disabled="busy" @click="handlePreview">
              {{ t('admin.accounts.kiroLoginPreviewButton') }}
            </button>
            <button class="btn btn-secondary" type="button" :disabled="busy" @click="handleCreateFromJSON">
              {{ t('admin.accounts.kiroLoginImportButton') }}
            </button>
          </div>
        </div>
      </details>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="busy" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button class="btn btn-primary" type="submit" form="kiro-login-form" :disabled="busy || !isAuthorized">
          {{ creating ? t('common.saving') : t('admin.accounts.kiroLoginCreateButton') }}
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
  KiroBuilderIDCreateAccountRequest,
  KiroBuilderIDDeviceFlowPollResponse,
  KiroBuilderIDDeviceFlowStartResponse,
  KiroCreateAccountRequest,
  KiroOAuthNormalizeResponse
} from '@/api/admin/accounts'

type KiroAccountDefaults = Omit<KiroBuilderIDCreateAccountRequest, 'session_id'>

interface Props {
  show: boolean
  accountDefaults?: KiroAccountDefaults
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
const region = ref('us-east-1')
const payloadText = ref('')
const preview = ref<KiroOAuthNormalizeResponse | null>(null)
const session = ref<KiroBuilderIDDeviceFlowStartResponse | null>(null)
const pollResult = ref<KiroBuilderIDDeviceFlowPollResponse | null>(null)
const pollTimer = ref<number | null>(null)

const busy = computed(() => creating.value || starting.value)
const isAuthorized = computed(() => pollResult.value?.status === 'authorized')
const verificationLink = computed(() => session.value?.verification_uri_complete || session.value?.verification_uri || '')
const createPayloadDefaults = computed(() => ({
  ...props.accountDefaults,
  name: name.value.trim() || props.accountDefaults?.name || undefined
}))
const statusText = computed(() => {
  if (isAuthorized.value) return t('admin.accounts.kiroLoginAuthorized')
  if (polling.value) return t('admin.accounts.kiroLoginPollingStatus')
  if (session.value) return t('admin.accounts.kiroLoginPendingStatus')
  return t('admin.accounts.kiroLoginNotStartedStatus')
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
  region.value = 'us-east-1'
  payloadText.value = ''
  preview.value = null
  session.value = null
  pollResult.value = null
}

const stopPolling = () => {
  if (pollTimer.value != null) {
    window.clearTimeout(pollTimer.value)
    pollTimer.value = null
  }
  polling.value = false
}

const scheduleNextPoll = (seconds?: number) => {
  stopPolling()
  if (!session.value || isAuthorized.value) return
  const delayMs = Math.max(1, seconds ?? session.value.interval ?? 5) * 1000
  pollTimer.value = window.setTimeout(() => {
    handlePollOnce(true)
  }, delayMs)
}

const parsePayload = () => {
  const trimmed = payloadText.value.trim()
  if (!trimmed) {
    throw new Error(t('admin.accounts.kiroLoginPayloadRequired'))
  }
  return JSON.parse(trimmed)
}

const handleClose = () => {
  if (busy.value || polling.value) return
  emit('close')
}

const openVerificationPage = () => {
  if (!verificationLink.value) return
  window.open(verificationLink.value, '_blank', 'noopener,noreferrer')
}

const handleStart = async () => {
  starting.value = true
  stopPolling()
  pollResult.value = null
  try {
    session.value = await adminAPI.accounts.startKiroBuilderIDDeviceFlow({
      region: region.value.trim() || 'us-east-1'
    })
    region.value = session.value.region
    appStore.showSuccess(t('admin.accounts.kiroLoginStartSuccess'))
    openVerificationPage()
    scheduleNextPoll(session.value.interval)
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.kiroLoginStartFailed'))
  } finally {
    starting.value = false
  }
}

const handlePollOnce = async (silent = false) => {
  if (!session.value) return
  polling.value = true
  try {
    const result = await adminAPI.accounts.pollKiroBuilderIDDeviceFlow({
      session_id: session.value.session_id
    })
    pollResult.value = result
    if (result.status === 'authorized') {
      stopPolling()
      if (!silent) {
        appStore.showSuccess(t('admin.accounts.kiroLoginAuthorizedSuccess'))
      }
      return
    }
    scheduleNextPoll(result.interval || session.value.interval)
  } catch (error: any) {
    stopPolling()
    appStore.showError(error?.message || t('admin.accounts.kiroLoginPollFailed'))
  } finally {
    if (!pollTimer.value) {
      polling.value = false
    }
  }
}

const handleCreateFromDeviceFlow = async () => {
  if (!session.value || !isAuthorized.value) {
    appStore.showError(t('admin.accounts.kiroLoginAuthorizeFirst'))
    return
  }
  creating.value = true
  try {
    await adminAPI.accounts.createKiroBuilderIDAccount({
      ...createPayloadDefaults.value,
      session_id: session.value.session_id
    })
    appStore.showSuccess(t('admin.accounts.kiroLoginCreateSuccess'))
    emit('created')
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.kiroLoginCreateFailed'))
  } finally {
    creating.value = false
  }
}

const handlePreview = async () => {
  try {
    const data = parsePayload()
    preview.value = await adminAPI.accounts.normalizeKiroOAuthCredentials({ data })
  } catch (error: any) {
    const message = error instanceof SyntaxError
      ? t('admin.accounts.dataImportParseFailed')
      : error?.message || t('admin.accounts.kiroLoginPreviewFailed')
    appStore.showError(message)
  }
}

const handleCreateFromJSON = async () => {
  creating.value = true
  try {
    const data = parsePayload()
    const defaults: Omit<KiroCreateAccountRequest, 'data'> = createPayloadDefaults.value
    await adminAPI.accounts.createKiroOAuthAccount({
      ...defaults,
      data
    })
    appStore.showSuccess(t('admin.accounts.kiroLoginImportSuccess'))
    emit('created')
  } catch (error: any) {
    const message = error instanceof SyntaxError
      ? t('admin.accounts.dataImportParseFailed')
      : error?.message || t('admin.accounts.kiroLoginCreateFailed')
    appStore.showError(message)
  } finally {
    creating.value = false
  }
}
</script>
