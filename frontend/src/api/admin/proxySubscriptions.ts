/**
 * Admin Proxy Subscriptions API
 * Embedded airport subscription sources → local mihomo → sidecar-* proxies
 */

import { apiClient } from '../client'

export type ProxySubscriptionSourceType = 'url' | 'inline'
export type ProxySubscriptionProtocol = 'socks5' | 'socks5h' | 'http' | 'https'
export type ProxySubscriptionSyncStatus = '' | 'ok' | 'error' | 'running'

export interface ProxySubscription {
  id: number
  name: string
  enabled: boolean
  source_type: ProxySubscriptionSourceType
  subscription_url_masked: string
  has_inline_body: boolean
  name_prefix: string
  protocol: ProxySubscriptionProtocol
  bind_address: string
  base_port: number
  max_ports: number
  sync_interval_sec: number
  node_allow_contains: string[]
  last_sync_at: string | null
  last_sync_status: ProxySubscriptionSyncStatus | string
  last_sync_error: string
  last_config_hash: string
  desired_count: number
  created_by: number
  next_due_at: string | null
  created_at: string
  updated_at: string
}

export interface ProxySubscriptionListParams {
  page?: number
  page_size?: number
  enabled?: boolean
  search?: string
}

export interface ProxySubscriptionListResponse {
  items: ProxySubscription[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface ProxySubscriptionCreateParams {
  name: string
  enabled?: boolean
  source_type?: ProxySubscriptionSourceType
  subscription_url?: string
  inline_body?: string
  name_prefix?: string
  protocol?: ProxySubscriptionProtocol
  bind_address?: string
  base_port?: number
  max_ports?: number
  sync_interval_sec?: number
  node_allow_contains?: string[]
}

export type ProxySubscriptionUpdateParams = Partial<ProxySubscriptionCreateParams>

export interface ProxySubscriptionSyncResult {
  desired_count: number
  config_hash: string
  created: number
  updated: number
  unchanged: number
  deleted: number
  skipped: number
  engine_running: boolean
  engine_skipped: boolean
  message?: string
}

export interface ProxySubscriptionEngineSourceStatus {
  id: number
  name: string
  name_prefix: string
  running: boolean
  config_hash: string
  base_port: number
  max_ports: number
  last_error?: string
}

export interface ProxySubscriptionEngineStatus {
  binary_path: string
  binary_found: boolean
  data_dir: string
  running_count: number
  sources: ProxySubscriptionEngineSourceStatus[]
}

export async function list(
  params: ProxySubscriptionListParams = {},
  options?: { signal?: AbortSignal }
): Promise<ProxySubscriptionListResponse> {
  const { data } = await apiClient.get<ProxySubscriptionListResponse>('/admin/proxy-subscriptions', {
    params,
    signal: options?.signal
  })
  return data
}

export async function get(id: number): Promise<ProxySubscription> {
  const { data } = await apiClient.get<ProxySubscription>(`/admin/proxy-subscriptions/${id}`)
  return data
}

export async function create(params: ProxySubscriptionCreateParams): Promise<ProxySubscription> {
  const { data } = await apiClient.post<ProxySubscription>('/admin/proxy-subscriptions', params)
  return data
}

export async function update(
  id: number,
  params: ProxySubscriptionUpdateParams
): Promise<ProxySubscription> {
  const { data } = await apiClient.put<ProxySubscription>(`/admin/proxy-subscriptions/${id}`, params)
  return data
}

export async function del(id: number): Promise<{ id: number }> {
  const { data } = await apiClient.delete<{ id: number }>(`/admin/proxy-subscriptions/${id}`)
  return data
}

export async function sync(id: number): Promise<ProxySubscriptionSyncResult> {
  const { data } = await apiClient.post<ProxySubscriptionSyncResult>(
    `/admin/proxy-subscriptions/${id}/sync`,
    undefined,
    { timeout: 180000 }
  )
  return data
}

export async function engineStatus(options?: {
  signal?: AbortSignal
}): Promise<ProxySubscriptionEngineStatus> {
  const { data } = await apiClient.get<ProxySubscriptionEngineStatus>(
    '/admin/proxy-subscriptions/engine/status',
    { signal: options?.signal }
  )
  return data
}

export const proxySubscriptionsAPI = {
  list,
  get,
  create,
  update,
  del,
  sync,
  engineStatus
}

export default proxySubscriptionsAPI
