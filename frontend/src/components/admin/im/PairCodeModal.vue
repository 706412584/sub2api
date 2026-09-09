<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-sm bg-white dark:bg-gray-800 rounded-xl shadow-xl">
      <div class="px-5 py-4 border-b border-gray-200 dark:border-gray-700">
        <h3 class="text-base font-semibold text-gray-900 dark:text-gray-100">
          {{ t('admin.imBots.pair.title', { name: bot.name }) }}
        </h3>
      </div>
      <div class="px-5 py-6 text-center">
        <div v-if="loading" class="py-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <template v-else-if="pairCode">
          <div class="text-4xl font-mono font-bold tracking-[0.3em] text-gray-900 dark:text-gray-100 select-all">
            {{ pairCode.code }}
          </div>
          <p class="mt-3 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imBots.pair.hint') }}</p>
          <p class="mt-1 text-xs text-gray-400">
            {{ t('admin.imBots.pair.expiresAt', { time: expiresAtText }) }}
          </p>
          <button class="mt-4 px-4 py-1.5 text-sm rounded-lg border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" @click="copy">
            {{ t('common.copy') }}
          </button>
        </template>
        <div v-else class="py-6 text-sm text-red-500">{{ error || t('admin.imBots.pair.failed') }}</div>
      </div>
      <div class="px-5 py-3 border-t border-gray-200 dark:border-gray-700 flex justify-end">
        <button class="px-4 py-1.5 text-sm rounded-lg border border-gray-300 dark:border-gray-600" @click="$emit('close')">
          {{ t('common.close') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { generateIMBotPairCode } from '@/api/admin/imBots'
import type { IMBot, IMBotPairCode } from '@/types'

const props = defineProps<{ bot: IMBot }>()
defineEmits<{ close: [] }>()

const { t } = useI18n()
const loading = ref(true)
const error = ref('')
const pairCode = ref<IMBotPairCode | null>(null)

const expiresAtText = computed(() =>
  pairCode.value ? new Date(pairCode.value.expires_at).toLocaleString() : ''
)

async function generate() {
  loading.value = true
  try {
    pairCode.value = await generateIMBotPairCode(props.bot.id)
  } catch (err) {
    const resp = (err as { response?: { data?: { message?: string } } })?.response?.data
    error.value = resp?.message || ''
  } finally {
    loading.value = false
  }
}

async function copy() {
  if (!pairCode.value) return
  try {
    await navigator.clipboard.writeText(pairCode.value.code)
  } catch {
    /* clipboard denied; code is selectable */
  }
}

onMounted(() => {
  void generate()
})
</script>
