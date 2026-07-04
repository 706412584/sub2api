<template>
  <BaseDialog
    :show="visible"
    :title="t('admin.accounts.batchScheduledTest.title')"
    width="wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-500 dark:bg-dark-700">
        <div class="text-sm font-medium text-gray-900 dark:text-gray-100">
          {{ t('admin.accounts.batchScheduledTest.selectedCount', { count: accountIds.length }) }}
        </div>
        <div v-if="accounts.length > 0" class="mt-2 flex flex-wrap gap-2">
          <span
            v-for="account in accounts.slice(0, 8)"
            :key="account.id"
            class="rounded bg-white px-2 py-1 text-xs text-gray-600 dark:bg-dark-600 dark:text-gray-300"
          >
            {{ account.name }}
          </span>
          <span
            v-if="accounts.length > 8"
            class="rounded bg-white px-2 py-1 text-xs text-gray-500 dark:bg-dark-600 dark:text-gray-400"
          >
            {{ t('admin.accounts.batchScheduledTest.moreSelected', { count: accounts.length - 8 }) }}
          </span>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div class="space-y-1.5">
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.scheduledTests.model') }}
          </label>
          <Select
            v-model="form.model_id"
            :options="availableModels"
            :disabled="loadingModels || submitting"
            value-key="id"
            label-key="display_name"
            :placeholder="loadingModels ? t('common.loading') + '...' : t('admin.accounts.selectTestModel')"
          />
          <p v-if="modelLoadError" class="text-xs text-red-500">{{ modelLoadError }}</p>
        </div>

        <div class="space-y-1.5">
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.scheduledTests.cronExpression') }}
          </label>
          <Input
            v-model="form.cron_expression"
            :disabled="submitting"
            :placeholder="'*/30 * * * *'"
            :hint="t('admin.scheduledTests.cronHelp')"
          />
        </div>

        <div class="space-y-1.5">
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.scheduledTests.maxResults') }}
          </label>
          <Input
            v-model="form.max_results"
            type="number"
            :disabled="submitting"
            placeholder="50"
          />
        </div>

        <div class="flex flex-col justify-end gap-3">
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <Toggle v-model="form.enabled" :disabled="submitting" />
            {{ t('admin.scheduledTests.enabled') }}
          </label>
          <div>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <Toggle v-model="form.auto_recover" :disabled="submitting" />
              {{ t('admin.scheduledTests.autoRecover') }}
            </label>
            <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
              {{ t('admin.scheduledTests.autoRecoverHelp') }}
            </p>
          </div>
        </div>
      </div>

      <div v-if="submitError" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-600 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-300">
        {{ submitError }}
      </div>

      <div v-if="result" class="space-y-3">
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-500">
          <div class="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {{ t('admin.accounts.batchScheduledTest.summary', { created: result.created, updated: result.updated, failed: result.failed }) }}
          </div>
        </div>

        <div class="max-h-80 overflow-y-auto rounded-lg border border-gray-200 dark:border-dark-500">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-500">
            <thead class="bg-gray-50 dark:bg-dark-700">
              <tr>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchScheduledTest.accountName') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchScheduledTest.action') }}</th>
                <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.batchScheduledTest.error') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-600 dark:bg-dark-800">
              <tr v-for="item in result.items" :key="item.account_id">
                <td class="px-3 py-2 text-gray-900 dark:text-gray-100">{{ accountName(item.account_id) }}</td>
                <td class="px-3 py-2">
                  <span :class="actionClass(item.action)">
                    {{ actionLabel(item.action) }}
                  </span>
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-300">{{ item.error || '-' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button class="btn btn-secondary" :disabled="submitting" @click="handleClose">
          {{ t('common.close') }}
        </button>
        <button
          class="btn btn-primary"
          :disabled="submitting || loadingModels || !form.model_id || !form.cron_expression || accountIds.length === 0"
          @click="handleSubmit"
        >
          {{ submitting ? t('admin.accounts.batchScheduledTest.submitting') : t('admin.accounts.batchScheduledTest.submit') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Input from '@/components/common/Input.vue'
import Toggle from '@/components/common/Toggle.vue'
import { adminAPI } from '@/api/admin'
import type { Account, BatchUpsertScheduledTestPlansResponse, ClaudeModel } from '@/types'

const props = defineProps<{
  visible: boolean
  accountIds: number[]
  accounts: Account[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update:visible', value: boolean): void
  (e: 'completed'): void
}>()

const { t } = useI18n()

const availableModels = ref<ClaudeModel[]>([])
const loadingModels = ref(false)
const modelLoadError = ref('')
const submitting = ref(false)
const submitError = ref('')
const result = ref<BatchUpsertScheduledTestPlansResponse | null>(null)
const prioritizedGeminiModels = ['gemini-3.1-flash-image', 'gemini-2.5-flash-image', 'gemini-3.5-flash', 'gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-3-flash-preview', 'gemini-3-pro-preview', 'gemini-2.0-flash']

const form = reactive({
  model_id: '',
  cron_expression: '*/30 * * * *',
  max_results: '50',
  enabled: true,
  auto_recover: false
})

const firstAccount = computed(() => props.accounts[0] ?? null)
const accountNameMap = computed(() => new Map(props.accounts.map(account => [account.id, account.name])))

const sortTestModels = (models: ClaudeModel[]) => {
  const priorityMap = new Map(prioritizedGeminiModels.map((id, index) => [id, index]))
  return [...models].sort((a, b) => {
    const aPriority = priorityMap.get(a.id) ?? Number.MAX_SAFE_INTEGER
    const bPriority = priorityMap.get(b.id) ?? Number.MAX_SAFE_INTEGER
    if (aPriority !== bPriority) return aPriority - bPriority
    return 0
  })
}

const resetForm = () => {
  form.model_id = ''
  form.cron_expression = '*/30 * * * *'
  form.max_results = '50'
  form.enabled = true
  form.auto_recover = false
}

const loadAvailableModels = async () => {
  if (!firstAccount.value) return
  loadingModels.value = true
  modelLoadError.value = ''
  form.model_id = ''
  try {
    const models = await adminAPI.accounts.getAvailableModels(firstAccount.value.id)
    availableModels.value = firstAccount.value.platform === 'gemini' || firstAccount.value.platform === 'antigravity'
      ? sortTestModels(models)
      : models
    if (availableModels.value.length > 0) {
      if (firstAccount.value.platform === 'gemini') {
        form.model_id = availableModels.value[0].id
      } else {
        const sonnetModel = availableModels.value.find(model => model.id.includes('sonnet'))
        form.model_id = sonnetModel?.id || availableModels.value[0].id
      }
    }
  } catch (error) {
    console.error('Failed to load available models:', error)
    availableModels.value = []
    form.model_id = ''
    modelLoadError.value = t('admin.accounts.batchScheduledTest.loadModelsFailed')
  } finally {
    loadingModels.value = false
  }
}

watch(
  () => props.visible,
  async (visible) => {
    if (visible) {
      result.value = null
      submitError.value = ''
      resetForm()
      await loadAvailableModels()
    }
  }
)

watch(
  () => props.accountIds,
  () => {
    if (props.visible) {
      result.value = null
      submitError.value = ''
    }
  }
)

const handleSubmit = async () => {
  if (submitting.value || !form.model_id || !form.cron_expression || props.accountIds.length === 0) return
  submitting.value = true
  submitError.value = ''
  try {
    result.value = await adminAPI.scheduledTests.batchUpsert({
      account_ids: props.accountIds,
      model_id: form.model_id,
      cron_expression: form.cron_expression,
      max_results: Number(form.max_results) || 50,
      enabled: form.enabled,
      auto_recover: form.auto_recover
    })
    emit('completed')
  } catch (error: any) {
    console.error('Failed to batch upsert scheduled test plans:', error)
    submitError.value = error?.message || String(error)
  } finally {
    submitting.value = false
  }
}

const accountName = (accountId: number) => accountNameMap.value.get(accountId) || `#${accountId}`

const actionLabel = (action: string) => {
  if (action === 'created') return t('admin.accounts.batchScheduledTest.created')
  if (action === 'updated') return t('admin.accounts.batchScheduledTest.updated')
  return t('admin.accounts.batchScheduledTest.failed')
}

const actionClass = (action: string) => {
  if (action === 'created') return 'text-green-600 dark:text-green-400'
  if (action === 'updated') return 'text-blue-600 dark:text-blue-400'
  return 'text-red-600 dark:text-red-400'
}

const handleClose = () => {
  emit('update:visible', false)
  emit('close')
}
</script>
