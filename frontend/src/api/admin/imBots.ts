import { apiClient } from '../index'
import type {
  IMBot,
  IMBotChat,
  IMBotMessage,
  IMBotPairCode,
  IMPlatform,
  PaginatedResponse,
} from '@/types'

export interface IMBotCreateRequest {
  name: string
  platform: string
  credentials: Record<string, string>
  api_key_id: number
  model_override?: string
  system_prompt?: string
  max_concurrency?: number
  history_max_messages?: number
  pairing_enabled?: boolean
}

export interface IMBotUpdateRequest {
  name?: string
  credentials?: Record<string, string> | null
  api_key_id?: number
  model_override?: string
  system_prompt?: string
  max_concurrency?: number
  history_max_messages?: number
  pairing_enabled?: boolean
}

export interface IMBotChatUpdateRequest {
  status?: 'active' | 'blocked'
  model_override?: string
}

function serializeCredentials(credentials: Record<string, string> | null | undefined): string | null | undefined {
  if (credentials === undefined) return undefined
  if (credentials === null) return null
  return JSON.stringify(credentials)
}

export async function listIMBots(
  page = 1,
  pageSize = 20,
  filters?: { platform?: string; status?: string; search?: string }
): Promise<PaginatedResponse<IMBot>> {
  const { data } = await apiClient.get<PaginatedResponse<IMBot>>('/admin/im-bots', {
    params: { page, page_size: pageSize, ...filters }
  })
  return data
}

export async function getIMBot(id: number): Promise<IMBot> {
  const { data } = await apiClient.get<{ data: IMBot }>(`/admin/im-bots/${id}`)
  return data.data
}

export async function listIMPlatforms(): Promise<IMPlatform[]> {
  const { data } = await apiClient.get<{ data: { platforms: IMPlatform[] } }>('/admin/im-bots/platforms')
  return data.data.platforms ?? []
}

export async function createIMBot(request: IMBotCreateRequest): Promise<IMBot> {
  const { credentials, ...rest } = request
  const { data } = await apiClient.post<{ data: IMBot }>('/admin/im-bots', {
    ...rest,
    credentials: serializeCredentials(credentials)
  })
  return data.data
}

export async function updateIMBot(id: number, request: IMBotUpdateRequest): Promise<IMBot> {
  const { credentials, ...rest } = request
  const { data } = await apiClient.put<{ data: IMBot }>(`/admin/im-bots/${id}`, {
    ...rest,
    credentials: serializeCredentials(credentials)
  })
  return data.data
}

export async function deleteIMBot(id: number): Promise<void> {
  await apiClient.delete(`/admin/im-bots/${id}`)
}

export async function testIMBot(id: number): Promise<{ success: boolean; message: string }> {
  const { data } = await apiClient.post<{ data: { success: boolean; message: string } }>(
    `/admin/im-bots/${id}/test`
  )
  return data.data
}

export async function enableIMBot(id: number): Promise<IMBot> {
  const { data } = await apiClient.post<{ data: IMBot }>(`/admin/im-bots/${id}/enable`)
  return data.data
}

export async function disableIMBot(id: number): Promise<IMBot> {
  const { data } = await apiClient.post<{ data: IMBot }>(`/admin/im-bots/${id}/disable`)
  return data.data
}

export async function generateIMBotPairCode(id: number): Promise<IMBotPairCode> {
  const { data } = await apiClient.post<{ data: IMBotPairCode }>(`/admin/im-bots/${id}/pair-codes`)
  return data.data
}

export async function listIMBotChats(id: number, page = 1, pageSize = 20): Promise<PaginatedResponse<IMBotChat>> {
  const { data } = await apiClient.get<PaginatedResponse<IMBotChat>>(`/admin/im-bots/${id}/chats`, {
    params: { page, page_size: pageSize }
  })
  return data
}

export async function updateIMBotChat(
  id: number,
  chatId: number,
  request: IMBotChatUpdateRequest
): Promise<void> {
  await apiClient.put(`/admin/im-bots/${id}/chats/${chatId}`, request)
}

export async function unpairIMBotChat(id: number, chatId: number): Promise<void> {
  await apiClient.delete(`/admin/im-bots/${id}/chats/${chatId}`)
}

export async function listIMBotChatMessages(
  id: number,
  chatId: number,
  page = 1,
  pageSize = 50
): Promise<PaginatedResponse<IMBotMessage>> {
  const { data } = await apiClient.get<PaginatedResponse<IMBotMessage>>(
    `/admin/im-bots/${id}/chats/${chatId}/messages`,
    { params: { page, page_size: pageSize } }
  )
  return data
}

export const imBotsApi = {
  list: listIMBots,
  get: getIMBot,
  platforms: listIMPlatforms,
  create: createIMBot,
  update: updateIMBot,
  delete: deleteIMBot,
  test: testIMBot,
  enable: enableIMBot,
  disable: disableIMBot,
  generatePairCode: generateIMBotPairCode,
  chats: listIMBotChats,
  updateChat: updateIMBotChat,
  unpairChat: unpairIMBotChat,
  chatMessages: listIMBotChatMessages
}
