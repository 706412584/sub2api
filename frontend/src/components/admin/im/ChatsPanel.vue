<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-2xl bg-white dark:bg-gray-800 rounded-xl shadow-xl max-h-[85vh] flex flex-col">
      <div class="px-5 py-4 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
        <h3 class="text-base font-semibold text-gray-900 dark:text-gray-100">
          {{ t('admin.imBots.chats.title', { name: bot.name }) }}
        </h3>
        <button class="text-gray-400 hover:text-gray-600" @click="$emit('close')">✕</button>
      </div>
      <div class="flex-1 overflow-y-auto px-5 py-3">
        <table v-if="chats.length" class="min-w-full text-sm">
          <thead>
            <tr class="text-left text-xs text-gray-500 uppercase">
              <th class="py-2 pr-3">{{ t('admin.imBots.chats.user') }}</th>
              <th class="py-2 pr-3">{{ t('admin.imBots.chats.platformUser') }}</th>
              <th class="py-2 pr-3">{{ t('admin.imBots.chats.status') }}</th>
              <th class="py-2 pr-3">{{ t('admin.imBots.chats.lastMessage') }}</th>
              <th class="py-2 pr-3 text-right">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
            <tr v-for="chat in chats" :key="chat.id">
              <td class="py-2 pr-3">{{ chat.display_name || '—' }}</td>
              <td class="py-2 pr-3 font-mono text-xs">{{ chat.platform_user_id }}</td>
              <td class="py-2 pr-3">
                <span
                  class="px-2 py-0.5 rounded text-xs"
                  :class="chat.status === 'active'
                    ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
                    : 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'"
                >
                  {{ t(`admin.imBots.chats.${chat.status}`) }}
                </span>
              </td>
              <td class="py-2 pr-3 text-xs text-gray-500">{{ chat.last_message_at ? new Date(chat.last_message_at).toLocaleString() : '—' }}</td>
              <td class="py-2 pr-3 text-right whitespace-nowrap">
                <button class="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 mr-1" @click="viewHistory(chat)">
                  {{ t('admin.imBots.chats.history') }}
                </button>
                <button class="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 mr-1" @click="toggleBlock(chat)">
                  {{ chat.status === 'active' ? t('admin.imBots.chats.block') : t('admin.imBots.chats.unblock') }}
                </button>
                <button class="text-xs px-2 py-1 rounded border border-red-300 text-red-600" @click="unpair(chat)">
                  {{ t('admin.imBots.chats.unpair') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="py-10 text-center text-sm text-gray-500">{{ loading ? t('common.loading') : t('admin.imBots.chats.empty') }}</div>
      </div>
    </div>

    <!-- history modal -->
    <div v-if="historyChat" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4" @click.self="historyChat = null">
      <div class="w-full max-w-xl bg-white dark:bg-gray-800 rounded-xl shadow-xl max-h-[80vh] flex flex-col">
        <div class="px-5 py-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {{ t('admin.imBots.chats.historyTitle', { user: historyChat.display_name || historyChat.platform_user_id }) }}
          </h4>
          <button class="text-gray-400 hover:text-gray-600" @click="historyChat = null">✕</button>
        </div>
        <div class="flex-1 overflow-y-auto px-5 py-3 space-y-3">
          <div v-if="!messages.length" class="py-8 text-center text-sm text-gray-500">{{ t('admin.imBots.chats.empty') }}</div>
          <div
            v-for="msg in messages"
            :key="msg.id"
            class="flex"
            :class="msg.role === 'user' ? 'justify-start' : 'justify-end'"
          >
            <div
              class="max-w-[80%] px-3 py-2 rounded-lg text-sm whitespace-pre-wrap break-words"
              :class="msg.role === 'user'
                ? 'bg-gray-100 dark:bg-gray-700 text-gray-900 dark:text-gray-100'
                : 'bg-blue-600 text-white'"
            >
              {{ msg.content }}
              <div class="mt-1 text-[10px] opacity-60">{{ new Date(msg.created_at).toLocaleString() }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { imBotsApi, unpairIMBotChat, updateIMBotChat } from '@/api/admin/imBots'
import type { IMBot, IMBotChat, IMBotMessage } from '@/types'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ bot: IMBot }>()
defineEmits<{ close: [] }>()

const { t } = useI18n()
const appStore = useAppStore()
const chats = ref<IMBotChat[]>([])
const loading = ref(true)
const historyChat = ref<IMBotChat | null>(null)
const messages = ref<IMBotMessage[]>([])

async function load() {
  loading.value = true
  try {
    const resp = await imBotsApi.chats(props.bot.id, 1, 100)
    chats.value = resp.items
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function viewHistory(chat: IMBotChat) {
  try {
    const resp = await imBotsApi.chatMessages(props.bot.id, chat.id, 1, 200)
    messages.value = resp.items
    historyChat.value = chat
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.loadFailed')))
  }
}

async function toggleBlock(chat: IMBotChat) {
  try {
    await updateIMBotChat(props.bot.id, chat.id, {
      status: chat.status === 'active' ? 'blocked' : 'active'
    })
    await load()
  } catch (error) {
    appStore.showError(extractError(error, t('admin.imBots.toggleFailed')))
  }
}

async function unpair(chat: IMBotChat) {
  if (!window.confirm(t('admin.imBots.chats.unpairConfirm'))) return
  try {
    await unpairIMBotChat(props.bot.id, chat.id)
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
})
</script>
