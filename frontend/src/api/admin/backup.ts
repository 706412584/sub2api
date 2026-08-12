import { apiClient } from '../client'

export interface BackupS3Config {
  endpoint: string
  region: string
  bucket: string
  access_key_id: string
  secret_access_key?: string
  prefix: string
  force_path_style: boolean
}

export interface BackupScheduleConfig {
  enabled: boolean
  cron_expr: string
  retain_days: number
  retain_count: number
}

export interface BackupRecord {
  id: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  backup_type: string
  file_name: string
  s3_key: string
  /** s3 | local；旧记录可能为空，前端按 s3 兼容处理 */
  storage?: string
  parts?: BackupPart[]
  size_bytes: number
  triggered_by: string
  error_message?: string
  started_at: string
  finished_at?: string
  expires_at?: string
  progress?: string
  restore_status?: string
  restore_error?: string
  restored_at?: string
}

export interface BackupPart {
  index: number
  s3_key: string
  size_bytes: number
  sha256?: string
}

export interface BackupDownloadPart {
  index: number
  size_bytes: number
  url: string
}

/** 兼容本地 storage 代理下载与上游 S3/分卷下载 */
export interface BackupDownloadInfo {
  mode?: 'presign' | 'proxy'
  url?: string
  storage?: string
  parts?: BackupDownloadPart[]
}

export type BackupDownloadResponse = BackupDownloadInfo
}

export interface CreateBackupRequest {
  expire_days?: number
}

export interface TestS3Response {
  ok: boolean
  message: string
}

// S3 Config
export async function getS3Config(): Promise<BackupS3Config> {
  const { data } = await apiClient.get<BackupS3Config>('/admin/backups/s3-config')
  return data
}

export async function updateS3Config(config: BackupS3Config): Promise<BackupS3Config> {
  const { data } = await apiClient.put<BackupS3Config>('/admin/backups/s3-config', config)
  return data
}

export async function testS3Connection(config: BackupS3Config): Promise<TestS3Response> {
  const { data } = await apiClient.post<TestS3Response>('/admin/backups/s3-config/test', config)
  return data
}

// Async image object storage
//
// Shares the S3 client with backups, so `reuse_backup_s3` borrows the endpoint and
// credentials configured above and only keeps its own bucket/prefix.
export interface ImageStorageConfig {
  enabled: boolean
  reuse_backup_s3: boolean
  bucket: string
  prefix: string
  public_base_url: string
  presign_expiry_hours: number
  max_download_bytes: number
  endpoint: string
  region: string
  access_key_id: string
  secret_access_key?: string
  force_path_style: boolean
}

export interface ImageStorageConfigResponse {
  config: ImageStorageConfig
  secret_configured: boolean
}

export async function getImageStorageConfig(): Promise<ImageStorageConfigResponse> {
  const { data } = await apiClient.get<ImageStorageConfigResponse>('/admin/backups/image-storage')
  return data
}

export async function updateImageStorageConfig(
  config: ImageStorageConfig,
): Promise<ImageStorageConfig> {
  const { data } = await apiClient.put<ImageStorageConfig>('/admin/backups/image-storage', config)
  return data
}

export async function testImageStorageConnection(
  config: ImageStorageConfig,
): Promise<TestS3Response> {
  const { data } = await apiClient.post<TestS3Response>(
    '/admin/backups/image-storage/test',
    config,
  )
  return data
}

// Schedule
export async function getSchedule(): Promise<BackupScheduleConfig> {
  const { data } = await apiClient.get<BackupScheduleConfig>('/admin/backups/schedule')
  return data
}

export async function updateSchedule(config: BackupScheduleConfig): Promise<BackupScheduleConfig> {
  const { data } = await apiClient.put<BackupScheduleConfig>('/admin/backups/schedule', config)
  return data
}

// Backup operations
export async function createBackup(req?: CreateBackupRequest): Promise<BackupRecord> {
  const { data } = await apiClient.post<BackupRecord>('/admin/backups', req || {})
  return data
}

export async function listBackups(): Promise<{ items: BackupRecord[] }> {
  const { data } = await apiClient.get<{ items: BackupRecord[] }>('/admin/backups')
  return data
}

export async function getBackup(id: string): Promise<BackupRecord> {
  const { data } = await apiClient.get<BackupRecord>(`/admin/backups/${id}`)
  return data
}

export async function deleteBackup(id: string): Promise<void> {
  await apiClient.delete(`/admin/backups/${id}`)
}

export async function getDownloadURL(id: string): Promise<BackupDownloadInfo> {
  const { data } = await apiClient.get<BackupDownloadInfo>(`/admin/backups/${id}/download-url`)
  return data
}

/** 鉴权代理下载本地备份（返回 blob，由调用方触发浏览器保存） */
export async function downloadBackupFile(id: string): Promise<Blob> {
  const { data } = await apiClient.get<Blob>(`/admin/backups/${id}/download`, {
    responseType: 'blob',
    // 备份可能较大，放宽超时
    timeout: 10 * 60 * 1000,
  })
  return data
}

// Restore
export async function restoreBackup(id: string, password: string): Promise<BackupRecord> {
  const { data } = await apiClient.post<BackupRecord>(`/admin/backups/${id}/restore`, { password })
  return data
}

export const backupAPI = {
  getS3Config,
  updateS3Config,
  testS3Connection,
  getImageStorageConfig,
  updateImageStorageConfig,
  testImageStorageConnection,
  getSchedule,
  updateSchedule,
  createBackup,
  listBackups,
  getBackup,
  deleteBackup,
  getDownloadURL,
  downloadBackupFile,
  restoreBackup,
}

export default backupAPI
