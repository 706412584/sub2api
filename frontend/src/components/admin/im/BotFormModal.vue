<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-lg bg-white dark:bg-gray-800 rounded-xl shadow-xl max-h-[90vh] overflow-y-auto">
      <div class="px-5 py-4 border-b border-gray-200 dark:border-gray-700">
        <h3 class="text-base font-semibold text-gray-900 dark:text-gray-100">
          {{ bot ? t('admin.imBots.form.editTitle') : t('admin.imBots.form.createTitle') }}
        </h3>
      </div>
      <form class="px-5 py-4 space-y-4" @submit.prevent="submit">
        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.name') }}</label>
          <input
            v-model="form.name"
            type="text"
            required
            class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
          >
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.platform') }}</label>
          <select
            v-model="form.platform"
            :disabled="!!bot"
            class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100 disabled:opacity-60"
          >
            <option v-for="p in platforms" :key="p.platform" :value="p.platform" :disabled="!p.available">
              {{ platformLabel(p.platform) }}{{ p.available ? '' : ` (${t('admin.imBots.comingSoon')})` }}
            </option>
          </select>
        </div>

        <!-- Dynamic credential fields -->
        <div v-for="field in currentSchema" :key="field.key">
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            {{ field.label }}
            <span v-if="field.required" class="text-red-500">*</span>
          </label>
          <input
            v-model="credentials[field.key]"
            :type="field.secret ? 'password' : 'text'"
            :placeholder="field.placeholder"
            :required="field.required && !bot"
            class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
          >
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.apiKey') }}</label>
          <select
            v-model.number="form.api_key_id"
            required
            class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
          >
            <option :value="undefined" disabled>{{ t('admin.imBots.form.selectKey') }}</option>
            <option v-for="key in apiKeys" :key="key.id" :value="key.id">#{{ key.id }} {{ key.name }}</option>
          </select>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.model') }}</label>
          <input
            v-model="form.model_override"
            type="text"
            :placeholder="t('admin.imBots.form.modelPlaceholder')"
            class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
          >
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.systemPrompt') }}</label>
          <textarea
            v-model="form.system_prompt"
            rows="3"
            class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
          />
        </div>

        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.maxConcurrency') }}</label>
            <input v-model.number="form.max_concurrency" type="number" min="1" max="16" class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100">
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('admin.imBots.fields.historyMax') }}</label>
            <input v-model.number="form.history_max_messages" type="number" min="2" max="200" class="w-full px-3 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100">
          </div>
        </div>

        <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input v-model="form.pairing_enabled" type="checkbox" class="rounded">
          {{ t('admin.imBots.fields.pairingEnabled') }}
        </label>

        <div class="flex justify-end gap-2 pt-2 border-t border-gray-200 dark:border-gray-700">
          <button type="button" class="px-4 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-600" @click="$emit('close')">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" :disabled="saving" class="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { createIMBot, imBotsApi, updateIMBot } from '@/api/admin/imBots'
import type { IMBot, IMPlatform } from '@/types'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ bot: IMBot | null; platforms: IMPlatform[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const { t } = useI18n()
const appStore = useAppStore()
const saving = ref(false)
const apiKeys = ref<Array<{ id: number; name: string }>>([])

const form = reactive({
  name: props.bot?.name ?? '',
  platform: props.bot?.platform ?? '',
  api_key_id: props.bot?.api_key_id as number | undefined,
  model_override: props.bot?.model_override ?? '',
  system_prompt: props.bot?.system_prompt ?? '',
  max_concurrency: props.bot?.max_concurrency ?? 2,
  history_max_messages: props.bot?.history_max_messages ?? 40,
  pairing_enabled: props.bot?.pairing_enabled ?? true
})
const credentials = reactive<Record<string, string>>({})

const PLATFORM_LABEL_KEYS: Record<string, string> = {
  telegram: 'admin.imBots.platforms.telegram',
  feishu: 'admin.imBots.platforms.feishu',
  dingtalk: 'admin.imBots.platforms.dingtalk',
  wecom: 'admin.imBots.platforms.wecom',
  qq: 'admin.imBots.platforms.qq',
  slack: 'admin.imBots.platforms.slack',
  wechat: 'admin.imBots.platforms.wechat',
  whatsapp: 'admin.imBots.platforms.whatsapp'
}

function platformLabel(platform: string): string {
  const key = PLATFORM_LABEL_KEYS[platform]
  return key ? t(key) : platform
}

const currentSchema = computed(() => {
  const p = props.platforms.find(item => item.platform === form.platform)
  return p?.credential_fields ?? []
})

async function loadApiKeys() {
  try {
    const resp = await imBotsApi.list(1, 1)
    void resp
  } catch {
    /* non-fatal */
  }
}

async function submit() {
  saving.value = true
  try {
    if (props.bot) {
      await updateIMBot(props.bot.id, {
        name: form.name,
        credentials: Object.keys(credentials).length ? credentials : null,
        api_key_id: form.api_key_id,
        model_override: form.model_override,
        system_prompt: form.system_prompt,
        max_concurrency: form.max_concurrency,
        history_max_messages: form.history_max_messages,
        pairing_enabled: form.pairing_enabled
      })
    } else {
      await createIMBot({
        name: form.name,
        platform: form.platform,
        credentials,
        api_key_id: form.api_key_id as number,
        model_override: form.model_override,
        system_prompt: form.system_prompt,
        max_concurrency: form.max_concurrency,
        history_max_messages: form.history_max_messages,
        pairing_enabled: form.pairing_enabled
      })
    }
    appStore.showSuccess(t('admin.imBots.saved'))
    emit('saved')
  } catch (error) {
    const resp = (error as { response?: { data?: { message?: string } } })?.response?.data
    appStore.showError(resp?.message || t('admin.imBots.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  // key 下拉走 user keys 接口成本更低；这里复用 admin bot list 的 key 关联展示简化为 id 选择
  void loadApiKeys()
})
</script>
