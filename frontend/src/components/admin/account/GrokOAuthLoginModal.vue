<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.grokOAuthTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="grok-oauth-login-form" class="space-y-4" @submit.prevent="handleCreate">
      <div class="rounded-lg border border-blue-200 bg-blue-50 px-3 py-2 text-sm text-blue-800 dark:border-blue-800/50 dark:bg-blue-900/20 dark:text-blue-200">
        {{ t('admin.accounts.grokOAuthDesc') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.accountName') }}</label>
        <input
          v-model="name"
          type="text"
          class="input"
          :placeholder="t('admin.accounts.grokOAuthNamePlaceholder')"
          :disabled="creating"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.grokOAuthRedirectUri') }}</label>
        <input v-model="redirectUri" type="text" class="input font-mono text-xs" :disabled="creating || Boolean(sessionId)" />
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.grokOAuthRedirectUriHint') }}
        </p>
      </div>

      <button class="btn btn-secondary" type="button" :disabled="creating" @click="handleStartLogin">
        {{ sessionId ? t('admin.accounts.grokOAuthOpenAgain') : t('admin.accounts.grokOAuthStartButton') }}
      </button>

      <div v-if="sessionId" class="rounded-xl border border-gray-200 p-3 text-sm dark:border-dark-700">
        <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.grokOAuthCallbackTitle') }}</div>
        <p class="mt-1 text-gray-600 dark:text-dark-300">
          {{ t('admin.accounts.grokOAuthCallbackHint') }}
        </p>
        <textarea
          v-model="callbackText"
          rows="4"
          class="mt-3 w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-gray-800 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-100"
          :placeholder="t('admin.accounts.grokOAuthCallbackPlaceholder')"
          spellcheck="false"
          :disabled="creating"
        />
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="creating" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button class="btn btn-primary" type="submit" form="grok-oauth-login-form" :disabled="creating || !sessionId">
          {{ creating ? t('common.saving') : t('admin.accounts.grokOAuthCreateButton') }}
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
const redirectUri = ref('http://127.0.0.1:56121/callback')
const authUrl = ref('')
const sessionId = ref('')
const state = ref('')
const callbackText = ref('')

const popupFeatures = computed(() => {
  const width = Math.min(960, (window.screen?.availWidth ?? 1000) - 40)
  const height = Math.min(820, (window.screen?.availHeight ?? 860) - 40)
  const left = Math.max(0, Math.floor(((window.screen?.availWidth ?? width) - width) / 2))
  const top = Math.max(0, Math.floor(((window.screen?.availHeight ?? height) - height) / 2))
  return `width=${width},height=${height},left=${left},top=${top},scrollbars=yes,resizable=yes`
})

watch(
  () => props.show,
  (show) => {
    if (show) {
      creating.value = false
      name.value = ''
      redirectUri.value = 'http://127.0.0.1:56121/callback'
      authUrl.value = ''
      sessionId.value = ''
      state.value = ''
      callbackText.value = ''
    }
  }
)

const handleClose = () => {
  if (creating.value) return
  emit('close')
}

const handleStartLogin = async () => {
  try {
    if (!authUrl.value) {
      const result = await adminAPI.accounts.generateGrokAuthUrl({ redirect_uri: redirectUri.value.trim() })
      authUrl.value = result.auth_url
      sessionId.value = result.session_id
      state.value = result.state
    }
    const popup = window.open(authUrl.value, 'grok-oauth-login', popupFeatures.value)
    if (!popup) {
      window.location.href = authUrl.value
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.grokOAuthStartFailed'))
  }
}

const extractCode = () => {
  const trimmed = callbackText.value.trim()
  if (!trimmed) {
    throw new Error(t('admin.accounts.grokOAuthCallbackRequired'))
  }
  let parsed: URL | null = null
  try {
    parsed = new URL(trimmed)
  } catch {
    // Allow pasting the raw authorization code.
  }
  if (!parsed) return trimmed

  const callbackState = parsed.searchParams.get('state')
  if (callbackState && callbackState !== state.value) {
    throw new Error(t('admin.accounts.grokOAuthStateMismatch'))
  }
  const code = parsed.searchParams.get('code') || parsed.searchParams.get('callback_token')
  return code || trimmed
}

const handleCreate = async () => {
  creating.value = true
  try {
    const code = extractCode()
    await adminAPI.accounts.createGrokAccountFromOAuth({
      session_id: sessionId.value,
      state: state.value,
      code,
      redirect_uri: redirectUri.value.trim(),
      name: name.value.trim() || undefined,
      skip_default_group_bind: true
    })
    appStore.showSuccess(t('admin.accounts.grokOAuthCreateSuccess'))
    emit('created')
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.grokOAuthCreateFailed'))
  } finally {
    creating.value = false
  }
}
</script>
