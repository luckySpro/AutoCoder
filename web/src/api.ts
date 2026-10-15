import { reactive } from 'vue'
import { mockAssignments, mockProjects, mockRoles, mockTools, mockUsage } from './mock'
import type { Assignment, Project, RoleId, RoleInfo, Settings, ToolDetection, UsageRecord } from './types'

const ASSIGN_KEY = 'autocoder.assignments'
const SETTINGS_KEY = 'autocoder.settings'
export const DEFAULT_SETTINGS: Settings = { concurrency: 3, autoMerge: true }
export const MAX_CONCURRENCY = 5

function loadJSON<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

function saveJSON(key: string, v: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(v))
  } catch {
    /* 隐私模式下忽略 */
  }
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: { Accept: 'application/json' } })
  if (!res.ok) throw new Error(`${url} 返回 ${res.status}`)
  return res.json()
}

// 全局状态：工具检测、角色、分配。后端不可达时使用演示数据并标记 demo。
export const store = reactive({
  loading: false,
  demo: false,
  error: '',
  scannedAt: '',
  manifestPath: '',
  tools: [] as ToolDetection[],
  roles: [] as RoleInfo[],
  assignments: [] as Assignment[],
  projects: mockProjects as Project[],
  usage: mockUsage as UsageRecord[],
  settings: loadJSON<Settings>(SETTINGS_KEY) ?? { ...DEFAULT_SETTINGS },
})

export async function scanTools(refresh = false) {
  store.loading = true
  store.error = ''
  try {
    const t = await getJSON<{ tools: ToolDetection[]; scannedAt: string; manifestPath: string }>(
      `/api/tools${refresh ? '?refresh=1' : ''}`,
    )
    const r = await getJSON<{ roles: RoleInfo[]; assignments: Assignment[] | null }>('/api/roles')
    store.tools = t.tools
    store.scannedAt = t.scannedAt
    store.manifestPath = t.manifestPath
    store.roles = r.roles
    store.assignments = loadJSON<Assignment[]>(ASSIGN_KEY) ?? r.assignments ?? []
    store.demo = false
  } catch (e) {
    store.demo = true
    store.error = (e as Error).message
    store.tools = mockTools
    store.scannedAt = new Date().toISOString()
    store.manifestPath = '~/.autocoder/tools.yaml'
    store.roles = mockRoles
    store.assignments = loadJSON<Assignment[]>(ASSIGN_KEY) ?? structuredClone(mockAssignments)
  } finally {
    store.loading = false
  }
}

export function saveAssignments() {
  saveJSON(ASSIGN_KEY, store.assignments)
  saveJSON(SETTINGS_KEY, store.settings)
}

export function resetAssignments() {
  try {
    localStorage.removeItem(ASSIGN_KEY)
    localStorage.removeItem(SETTINGS_KEY)
  } catch {
    /* ignore */
  }
  store.settings = { ...DEFAULT_SETTINGS }
  return scanTools()
}

export function toolName(id: string) {
  return store.tools.find((t) => t.id === id)?.name ?? id
}

export function roleName(id: RoleId) {
  return store.roles.find((r) => r.id === id)?.name ?? id
}

export const fmtTokens = (n: number) =>
  n >= 1e6 ? `${(n / 1e6).toFixed(2)}M` : n >= 1e3 ? `${(n / 1e3).toFixed(1)}K` : String(n)

export const totalTokens = (u: { input: number; output: number; cacheRead: number; cacheWrite: number }) =>
  u.input + u.output + u.cacheRead + u.cacheWrite
