<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.kiroDetails.title')"
    width="wide"
    close-on-click-outside
    @close="emit('close')"
  >
    <div v-if="account" class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-indigo-50 p-4 dark:bg-indigo-900/20">
        <div>
          <div class="text-base font-semibold text-gray-900 dark:text-white">{{ account.name }}</div>
          <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            #{{ account.id }} · {{ account.type === 'apikey' ? 'API Key' : 'OAuth' }}
          </div>
        </div>
        <button class="btn btn-secondary" type="button" :disabled="loading" @click="load">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
          {{ t('admin.accounts.kiroDetails.refresh') }}
        </button>
      </div>

      <div v-if="loading" class="flex justify-center py-12">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>
      <div v-else-if="error" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-600 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
        {{ error }}
      </div>
      <template v-else>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <DetailCard :label="t('admin.accounts.kiroDetails.subscription')" :value="usage?.subscription_title || '-'" />
          <DetailCard :label="t('admin.accounts.kiroDetails.email')" :value="usage?.email || accountEmail || '-'" />
          <DetailCard :label="t('admin.accounts.kiroDetails.region')" :value="apiRegion" />
          <DetailCard :label="t('admin.accounts.kiroDetails.endpoint')" :value="endpoint" />
          <DetailCard :label="t('admin.accounts.kiroDetails.overage')" :value="overageLabel" />
          <DetailCard :label="t('admin.accounts.kiroDetails.nextReset')" :value="formatReset(usage?.next_reset_at)" />
        </div>

        <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
          <div class="flex items-center justify-between gap-3">
            <span class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.kiroDetails.quota') }}</span>
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ quotaText }}</span>
          </div>
          <div class="mt-3 h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
            <div class="h-full rounded-full bg-indigo-500 transition-all" :style="{ width: quotaPercent + '%' }"></div>
          </div>
          <div class="mt-1 text-right text-xs text-gray-500 dark:text-gray-400">{{ quotaPercent.toFixed(1) }}%</div>
        </div>

        <div>
          <div class="mb-2 flex items-center justify-between">
            <span class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.kiroDetails.models') }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ models.length }}</span>
          </div>
          <div class="max-h-64 overflow-auto rounded-xl border border-gray-200 p-3 dark:border-dark-700">
            <div v-if="models.length" class="flex flex-wrap gap-2">
              <span v-for="model in models" :key="model" class="rounded-md bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-200">
                {{ model }}
              </span>
            </div>
            <div v-else class="text-sm text-gray-400">-</div>
          </div>
        </div>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account, KiroUsageInfo } from '@/types'

const DetailCard = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(props) {
    return () => h('div', { class: 'rounded-xl border border-gray-200 p-3 dark:border-dark-700' }, [
      h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('div', { class: 'mt-1 break-all text-sm font-medium text-gray-900 dark:text-white' }, props.value)
    ])
  }
})

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const usage = ref<KiroUsageInfo | null>(null)
const models = ref<string[]>([])

const credentials = computed(() => props.account?.credentials || {})
const accountEmail = computed(() => String((props.account?.extra as Record<string, unknown> | undefined)?.email || ''))
const apiRegion = computed(() => String(credentials.value.api_region || credentials.value.region || 'us-east-1'))
const endpoint = computed(() => String(credentials.value.endpoint || 'cli').toUpperCase())
const quotaPercent = computed(() => {
  if (!usage.value || usage.value.usage_limit <= 0) return 0
  return Math.min(100, Math.max(0, usage.value.current_usage / usage.value.usage_limit * 100))
})
const quotaText = computed(() => `${formatNumber(usage.value?.current_usage || 0)} / ${formatNumber(usage.value?.usage_limit || 0)}`)
const overageLabel = computed(() => {
  if (usage.value?.overage_enabled === true) return t('admin.accounts.kiroDetails.enabled')
  if (usage.value?.overage_capable === true) return t('admin.accounts.kiroDetails.available')
  if (usage.value?.overage_capable === false) return t('admin.accounts.kiroDetails.unavailable')
  return t('admin.accounts.kiroDetails.unknown')
})

function formatNumber(value: number) {
  return Number.isInteger(value) ? value.toLocaleString() : value.toLocaleString(undefined, { maximumFractionDigits: 2 })
}
function formatReset(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}
async function load() {
  if (!props.account || loading.value) return
  loading.value = true
  error.value = ''
  try {
    const [usageResult, modelResult] = await Promise.all([
      adminAPI.accounts.getUsage(props.account.id, 'active', true),
      adminAPI.accounts.getAvailableModels(props.account.id)
    ])
    usage.value = usageResult.kiro || null
    models.value = modelResult.map(model => model.id)
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('admin.accounts.kiroDetails.loadFailed'))
  } finally {
    loading.value = false
  }
}
watch(() => [props.show, props.account?.id] as const, ([show]) => {
  if (show) void load()
  else {
    usage.value = null
    models.value = []
    error.value = ''
  }
})
</script>
