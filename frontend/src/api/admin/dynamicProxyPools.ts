import api from '@/api'
import type { DynamicProxyPool } from '@/types'

export interface DynamicProxyPoolListParams {
  page?: number
  page_size?: number
  search?: string
  enabled?: boolean
}

export interface CreateDynamicProxyPoolRequest {
  name: string
  source_type?: string
  subscription_id?: number | null
  extract_url?: string
  protocol?: string
  auth_mode?: string
  username?: string
  password?: string
  response_format?: string
  line_separator?: string
  ip_field_path?: string
  port_field_path?: string
  refresh_interval_sec?: number
  ip_duration_sec?: number
  extract_count?: number
  min_alive?: number
}

export interface UpdateDynamicProxyPoolRequest {
  name?: string
  enabled?: boolean
  source_type?: string
  subscription_id?: number | null
  extract_url?: string
  protocol?: string
  auth_mode?: string
  username?: string
  password?: string
  response_format?: string
  line_separator?: string
  ip_field_path?: string
  port_field_path?: string
  refresh_interval_sec?: number
  ip_duration_sec?: number
  extract_count?: number
  min_alive?: number
}

export interface DynamicProxyPoolExtractResult {
  created: number
  failed: number
  alive_count: number
}

export const dynamicProxyPoolsAPI = {
  async list(params: DynamicProxyPoolListParams = {}) {
    const q = new URLSearchParams()
    if (params.page) q.set('page', String(params.page))
    if (params.page_size) q.set('page_size', String(params.page_size))
    if (params.search) q.set('search', params.search)
    if (params.enabled !== undefined) q.set('enabled', params.enabled ? 'true' : 'false')
    const res = await api.get(`/api/v1/admin/dynamic-proxy-pools?${q.toString()}`)
    return res.data as { items: DynamicProxyPool[]; total: number; page: number; page_size: number }
  },

  async getById(id: number) {
    const res = await api.get(`/api/v1/admin/dynamic-proxy-pools/${id}`)
    return res.data as DynamicProxyPool
  },

  async create(data: CreateDynamicProxyPoolRequest) {
    const res = await api.post('/api/v1/admin/dynamic-proxy-pools', data)
    return res.data as DynamicProxyPool
  },

  async update(id: number, data: UpdateDynamicProxyPoolRequest) {
    const res = await api.put(`/api/v1/admin/dynamic-proxy-pools/${id}`, data)
    return res.data as DynamicProxyPool
  },

  async delete(id: number) {
    await api.delete(`/api/v1/admin/dynamic-proxy-pools/${id}`)
  },

  async extract(id: number) {
    const res = await api.post(`/api/v1/admin/dynamic-proxy-pools/${id}/extract`)
    return res.data as DynamicProxyPoolExtractResult
  },
}