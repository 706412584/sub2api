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
          <a v-if="pairCode.pair_url" :href="pairCode.pair_url" target="_blank" rel="noopener noreferrer" class="inline-block rounded-lg p-2 bg-white ring-1 ring-gray-200 dark:ring-gray-700 hover:ring-blue-400 transition">
            <canvas ref="qrCanvas" width="220" height="220"></canvas>
          </a>
          <p v-if="pairCode.pair_url" class="mt-3 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.imBots.pair.scanHint') }}
          </p>
          <template v-else>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imBots.pair.hint') }}</p>
          </template>
          <div class="mt-4 text-3xl font-mono font-bold tracking-[0.3em] text-gray-900 dark:text-gray-100 select-all">
            {{ pairCode.code }}
          </div>
          <p class="mt-2 text-xs text-gray-400">
            {{ t('admin.imBots.pair.expiresAt', { time: expiresAtText }) }}
          </p>
          <button class="mt-3 px-4 py-1.5 text-sm rounded-lg border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" @click="copy">
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
import QRCode from 'qrcode'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { generateIMBotPairCode } from '@/api/admin/imBots'
import type { IMBot, IMBotPairCode } from '@/types'

const props = defineProps<{ bot: IMBot }>()
defineEmits<{ close: [] }>()

const { t } = useI18n()
const loading = ref(true)
const error = ref('')
const pairCode = ref<IMBotPairCode | null>(null)
const qrCanvas = ref<HTMLCanvasElement | null>(null)

const expiresAtText = computed(() =>
  pairCode.value ? new Date(pairCode.value.expires_at).toLocaleString() : ''
)

async function renderQR() {
  await nextTick()
  if (!qrCanvas.value || !pairCode.value?.pair_url) return
  await QRCode.toCanvas(qrCanvas.value, pairCode.value.pair_url, {
    width: 220,
    margin: 2,
    errorCorrectionLevel: 'L',
  })
}

watch(() => pairCode.value?.pair_url, (url) => {
  if (url) void renderQR()
})

async function generate() {
  loading.value = true
  try {
    pairCode.value = await generateIMBotPairCode(props.bot.id)
    if (pairCode.value.pair_url) await renderQR()
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
