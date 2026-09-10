<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex items-center justify-between">
          <div>
            <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">
              {{ t('admin.imBots.title') }}
            </h2>
            <p class="text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.imBots.description') }}
            </p>
          </div>
          <button
            class="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50"
            :disabled="loading"
            @click="openCreate"
          >
            {{ t('admin.imBots.create') }}
          </button>
        </div>
      </template>

      <template #table>
        <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead class="bg-gray-50 dark:bg-gray-900/50">
            <tr>
              <th class="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase">{{ t('admin.imBots.fields.name') }}</th>
              <th class="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase">{{ t('admin.imBots.fields.platform') }}</th>
              <th class="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase">{{ t('admin.imBots.fields.apiKey') }}</th>
              <th class="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase">{{ t('admin.imBots.fields.model') }}</th>
              <th class="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase">{{ t('admin.imBots.statusLabel') }}</th>
              <th class="px-4 py-2.5 text-right text-xs font-medium text-gray-500 uppercase">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
            <tr v-if="loading">
              <td colspan="6" class="px-4 py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="bots.length === 0">
              <td colspan="6" class="px-4 py-8 text-center text-sm text-gray-500">{{ t('admin.imBots.empty') }}</td>
            </tr>
            <tr v-for="bot in bots" :key="bot.id" class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
              <td class="px-4 py-2.5 text-sm font-medium text-gray-900 dark:text-gray-100">{{ bot.name }}</td>
              <td class="px-4 py-2.5 text-sm text-gray-600 dark:text-gray-300">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300">
                  {{ bot.platform }}
                </span>
              </td>
              <td class="px-4 py-2.5 text-sm text-gray-600 dark:text-gray-300">#{{ bot.api_key_id }}</td>
              <td class="px-4 py-2.5 text-sm text-gray-600 dark:text-gray-300">{{ bot.model_override || t('admin.imBots.groupDefault') }}</td>
              <td class="px-4 py-2.5">
                <span
                  class="px-2 py-0.5 rounded text-xs font-medium"
                  :class="statusClass(bot.status)"
                  :title="bot.status === 'error' ? bot.last_error : ''"
                >
                  {{ t(`admin.imBots.status.${bot.status}`) }}
                </span>
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex items-center justify-end gap-1.5">
                  <button class="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" @click="handleTest(bot)">
                    {{ t('admin.imBots.actions.test') }}
                  </button>
                  <button
                    class="text-xs px-2 py-1 rounded border hover:bg-gray-100 dark:hover:bg-gray-700"
                    :class="bot.status === 'enabled' ? 'border-yellow-400 text-yellow-600' : 'border-green-500 text-green-600'"
                    @click="handleToggle(bot)"
                  >
                    {{ bot.status === 'enabled' ? t('admin.imBots.actions.disable') : t('admin.imBots.actions.enable') }}
                  </button>
                  <button class="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" @click="openPairCode(bot)">
                    {{ t('admin.imBots.actions.pairCode') }}
                  </button>
                  <button class="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" @click="openChats(bot)">
                    {{ t('admin.imBots.actions.chats') }}
                  </button>
                  <button class="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" @click="openEdit(bot)">
                    {{ t('common.edit') }}
                  </button>
                  <button class="text-xs px-2 py-1 rounded border border-red-300 text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20" @click="handleDelete(bot)">
                    {{ t('common.delete') }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </template>

      <template #pagination>
        <div v-if="pages > 1" class="flex items-center justify-between text-sm">
          <span class="text-gray-500">{{ t('common.pageOf', { page, pages }) }}</span>
          <div class="flex gap-2">
            <button class="px-3 py-1 rounded border border-gray-300 dark:border-gray-600 disabled:opacity-40" :disabled="page <= 1" @click="goPage(page - 1)">
              {{ t('common.prev') }}
            </button>
            <button class="px-3 py-1 rounded border border-gray-300 dark:border-gray-600 disabled:opacity-40" :disabled="page >= pages" @click="goPage(page + 1)">
              {{ t('common.next') }}
            </button>
          </div>
        </div>
      </template>
    </TablePageLayout>

    <BotFormModal
      v-if="showForm"
      :bot="editingBot"
      :platforms="platforms"
      @close="showForm = false"
      @saved="onSaved"
    />
    <PairCodeModal
      v-if="pairCodeBot"
      :bot="pairCodeBot"
      @close="pairCodeBot = null"
    />
    <ChatsPanel
      v-if="chatsBot"
      :bot="chatsBot"
      @close="chatsBot = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import {
  imBotsApi,
  listIMPlatforms,
  testIMBot,
  enableIMBot,
  disableIMBot,
  deleteIMBot
} from '@/api/admin/imBots'
import type { IMBot, IMPlatform } from '@/types'
import { useAppStore } from '@/stores/app'
import BotFormModal from '@/components/admin/im/BotFormModal.vue'
import PairCodeModal from '@/components/admin/im/PairCodeModal.vue'
import ChatsPanel from '@/components/admin/im/ChatsPanel.vue'

const { t } = useI18n()
const appStore = useAppStore()

const bots = ref<IMBot[]>([])
const platforms = ref<IMPlatform[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = 20
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const total = ref(0)

const showForm = ref(false)
const editingBot = ref<IMBot | null>(null)
const pairCodeBot = ref<IMBot | null>(null)
const chatsBot = ref<IMBot | null>(null)

const statusClass = (status: IMBot['status']) => {
  switch (status) {
    case 'enabled':
      return 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
    case 'error':
      return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
  }
}

async function load() {
  loading.value = true
  try {
    const resp = await imBotsApi.list(page.value, pageSize)
    bots.value = resp.items
    total.value = resp.total
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function loadPlatforms() {
  try {
    platforms.value = await listIMPlatforms()
  } catch {
    platforms.value = []
  }
}

function goPage(next: number) {
  page.value = next
  void load()
}

function openCreate() {
  editingBot.value = null
  showForm.value = true
}

function openEdit(bot: IMBot) {
  editingBot.value = bot
  showForm.value = true
}

function openPairCode(bot: IMBot) {
  pairCodeBot.value = bot
}

function openChats(bot: IMBot) {
  chatsBot.value = bot
}

function onSaved() {
  showForm.value = false
  void load()
}

async function handleTest(bot: IMBot) {
  try {
    const result = await testIMBot(bot.id)
    if (result.success) {
      appStore.showSuccess(t('admin.imBots.testOk'))
    } else {
      appStore.showError(result.message || t('admin.imBots.testFailed'))
    }
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.testFailed')))
  }
}

async function handleToggle(bot: IMBot) {
  try {
    if (bot.status === 'enabled') {
      await disableIMBot(bot.id)
    } else {
      await enableIMBot(bot.id)
    }
    await load()
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.toggleFailed')))
  }
}

async function handleDelete(bot: IMBot) {
  if (!window.confirm(t('admin.imBots.deleteConfirm', { name: bot.name }))) return
  try {
    await deleteIMBot(bot.id)
    await load()
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.deleteFailed')))
  }
}

function extractError(error: unknown, fallback: string): string {
  const resp = (error as { response?: { data?: { message?: string } } })?.response?.data
  return resp?.message || fallback
}

onMounted(() => {
  void load()
  void loadPlatforms()
})
</script>
