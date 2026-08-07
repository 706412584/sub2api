/** Shared 5-minute cache for subscription/pool node connectivity tests. */

export type NodeTestCacheEntry = {
  success: boolean
  latency_ms?: number
  message: string
  testedAt: number
  /** Optional pool-proxy fields (Grok probe + displayed status). */
  status?: string
  grok_reasoning_status?: string
  grok_reasoning_message?: string
}

export const NODE_TEST_CACHE_TTL_MS = 5 * 60 * 1000

const cache = new Map<string, NodeTestCacheEntry>()

export function nodeTestCacheKey(identity: string, server?: string, port?: string | number) {
  const id = (identity || '').trim()
  if (id) return id
  return `${String(server || '').trim()}:${String(port ?? '').trim()}`
}

/** Cache key for a dynamic-pool owned proxy test result. */
export function poolProxyTestCacheKey(proxyId: number | string) {
  const id = Number(proxyId)
  if (!id || id <= 0) return ''
  return `pool-proxy:${id}`
}

export function getFreshNodeTest(key: string, now = Date.now()): NodeTestCacheEntry | null {
  if (!key) return null
  const entry = cache.get(key)
  if (!entry?.testedAt) return null
  if (now - entry.testedAt >= NODE_TEST_CACHE_TTL_MS) return null
  return { ...entry }
}

export function setNodeTest(key: string, result: Omit<NodeTestCacheEntry, 'testedAt'> & { testedAt?: number }) {
  if (!key) return
  const entry: NodeTestCacheEntry = {
    success: !!result.success,
    latency_ms: result.latency_ms,
    message: result.message || '',
    testedAt: result.testedAt ?? Date.now(),
    status: result.status,
    grok_reasoning_status: result.grok_reasoning_status,
    grok_reasoning_message: result.grok_reasoning_message
  }
  cache.set(key, entry)
}

export function isNodeTestFresh(key: string, now = Date.now()) {
  return getFreshNodeTest(key, now) != null
}
