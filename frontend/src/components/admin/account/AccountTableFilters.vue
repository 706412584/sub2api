<template>
    <div v-show="moreFiltersOpen" :id="moreFiltersId" class="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-3">
      <Select :model-value="filters.type" :aria-label="t('admin.accounts.allTypes')" class="min-w-0 w-full" :options="tOpts" @update:model-value="updateType" @change="$emit('change')" />
      <Select :model-value="filters.privacy_mode" :aria-label="t('admin.accounts.allPrivacyModes')" class="min-w-0 w-full" :options="privacyOpts" @update:model-value="updatePrivacyMode" @change="$emit('change')" />
      <Select v-if="filters.platform === 'grok'" :model-value="filters.risk" :aria-label="t('admin.accounts.allRisk')" class="min-w-0 w-full" :options="riskOpts" @update:model-value="updateRisk" @change="$emit('change')" />
      <Select :model-value="filters.group" :aria-label="t('admin.accounts.allGroups')" class="min-w-0 w-full" :options="gOpts" @update:model-value="updateGroup" @change="$emit('change')" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, useId } from 'vue'; import { useI18n } from 'vue-i18n'; import Select from '@/components/common/Select.vue'; import SearchInput from '@/components/common/SearchInput.vue'
import type { AdminGroup } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
const props = defineProps<{ searchQuery: string; filters: Record<string, any>; groups?: AdminGroup[] }>()
const emit = defineEmits(['update:searchQuery', 'update:filters', 'change']); const { t } = useI18n()
const moreFiltersOpen = ref(false)
const moreFiltersId = useId()
const activeSecondaryCount = computed(() =>
  ['type', 'privacy_mode', 'group'].filter(key => {
    const value = props.filters[key]
    return value !== '' && value !== null && value !== undefined
  }).length
)
const updatePlatform = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, platform: value }) }
const updateType = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, type: value }) }
const updateStatus = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, status: value }) }
const updatePrivacyMode = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, privacy_mode: value }) }
const updateRisk = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, risk: value }) }
const updateGroup = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, group: value }) }
const pOpts = computed(() => [{ value: '', label: t('admin.accounts.allPlatforms') }, ...CONCRETE_PLATFORM_OPTIONS])
const tOpts = computed(() => [{ value: '', label: t('admin.accounts.allTypes') }, { value: 'oauth', label: t('admin.accounts.oauthType') }, { value: 'setup-token', label: t('admin.accounts.setupToken') }, { value: 'apikey', label: t('admin.accounts.apiKey') }, { value: 'bedrock', label: 'AWS Bedrock' }])
const sOpts = computed(() => [{ value: '', label: t('admin.accounts.allStatus') }, { value: 'active', label: t('admin.accounts.status.active') }, { value: 'inactive', label: t('admin.accounts.status.inactive') }, { value: 'error', label: t('admin.accounts.status.error') }, { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') }, { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') }, { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }])
const privacyOpts = computed(() => [
  { value: '', label: t('admin.accounts.allPrivacyModes') },
  { value: '__unset__', label: t('admin.accounts.privacyUnset') },
  { value: 'training_off', label: 'Privacy' },
  { value: 'training_set_cf_blocked', label: 'CF' },
  { value: 'training_set_failed', label: 'Fail' }
])
const riskOpts = computed(() => [
  { value: '', label: t('admin.accounts.allRisk') },
  { value: 'flagged', label: t('admin.accounts.botRisk') },
  { value: 'normal', label: t('admin.accounts.riskNormal') },
])
const gOpts = computed(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...(props.groups || []).map(g => ({ value: String(g.id), label: g.name }))
])
</script>