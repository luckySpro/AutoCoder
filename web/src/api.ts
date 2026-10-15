import { reactive } from 'vue'
import { mockAssignments, mockIdentity, mockLocal, mockProjects, mockRepos, mockRoles, mockRunning, mockTools, mockUsage } from './mock'
import type {
  Assignment, GitIdentity, GitRepo, LocalRepo, Project, RoleId, RoleInfo, RunningTask, Settings, ToolDetection, ToolSpec, UsageRecord,
} from './types'

const ASSIGN_KEY = 'autocoder.assignments'
const SETTINGS_KEY = 'autocoder.settings'
export const DEFAULT_SETTINGS: Settings = { concurrency: 3, autoMerge: true }
export const MAX_CONCURRENCY = 5
export const STRENGTHS = ['architecture', 'coding', 'frontend', 'testing', 'review']
export const strengthName: Record<string, string> = {
  architecture: '架构', coding: '编码', frontend: '前端', testing: '测试', review: '评审',
}

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

export class ApiError extends Error {
  constructor(message: string, public status: number) {
    super(message)
  }
}

async function call<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: { Accept: 'application/json', ...(method !== 'GET' ? { 'Content-Type': 'application/json', 'X-AutoCoder': '1' } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(data.error ?? `${url} 返回 ${res.status}`, res.status)
  return data as T
}

// 全局状态。连不上本机服务时（例如静态预览）使用演示数据并标记 demo。
export const store = reactive({
  loading: false,
  demo: false,
  scannedAt: '',
  manifestPath: '',
  tools: [] as ToolDetection[],
  roles: [] as RoleInfo[],
  assignments: [] as Assignment[],
  projects: mockProjects as Project[],
  usage: mockUsage as UsageRecord[],
  running: mockRunning as RunningTask[],
  settings: loadJSON<Settings>(SETTINGS_KEY) ?? { ...DEFAULT_SETTINGS },
})

type ToolsResp = { tools: ToolDetection[]; scannedAt: string; manifestPath: string }

function applyTools(t: ToolsResp) {
  store.tools = t.tools
  store.scannedAt = t.scannedAt
  store.manifestPath = t.manifestPath
}

export async function scanTools(refresh = false) {
  store.loading = true
  try {
    applyTools(await call<ToolsResp>('GET', `/api/tools${refresh ? '?refresh=1' : ''}`))
    const r = await call<{ roles: RoleInfo[]; assignments: Assignment[] | null }>('GET', '/api/roles')
    store.roles = r.roles
    store.assignments = loadJSON<Assignment[]>(ASSIGN_KEY) ?? r.assignments ?? []
    store.demo = false
  } catch {
    store.demo = true
    if (!store.tools.length) store.tools = structuredClone(mockTools)
    store.scannedAt = new Date().toISOString()
    store.manifestPath = '~/.autocoder/tools.yaml'
    store.roles = mockRoles
    store.assignments = loadJSON<Assignment[]>(ASSIGN_KEY) ?? structuredClone(mockAssignments)
  } finally {
    store.loading = false
  }
}

// ---------- 工具清单管理 ----------

export function specOf(t: ToolDetection): ToolSpec {
  const { id, name, vendor, commands, paths, versionArgs, headless, usageArgs, capability, strengths, disabled } = t
  return { id, name, vendor, commands, paths, versionArgs, headless, usageArgs, capability, strengths, disabled }
}

export async function saveTool(spec: ToolSpec) {
  if (store.demo) {
    const i = store.tools.findIndex((t) => t.id === spec.id)
    if (i >= 0) {
      const old = store.tools[i]
      store.tools[i] = { ...old, ...spec, source: old.source === 'custom' ? 'custom' : 'modified' }
    } else {
      store.tools.push({ ...spec, source: 'custom', installed: false })
    }
    return
  }
  applyTools(await call<ToolsResp>('PUT', `/api/tools/${encodeURIComponent(spec.id)}`, spec))
}

// 自定义工具：删除；内置工具：恢复默认
export async function removeTool(id: string) {
  if (store.demo) {
    const i = store.tools.findIndex((t) => t.id === id)
    const builtin = mockTools.find((t) => t.id === id)
    if (builtin) store.tools[i] = structuredClone(builtin)
    else if (i >= 0) store.tools.splice(i, 1)
    return
  }
  applyTools(await call<ToolsResp>('DELETE', `/api/tools/${encodeURIComponent(id)}`))
}

export async function importTools(yaml: string) {
  if (store.demo) throw new ApiError('预览模式不能导入，请在本机运行 AutoCoder', 0)
  applyTools(await call<ToolsResp>('POST', '/api/tools/import', { yaml }))
}

// ---------- 添加项目：本机 git / GitHub ----------

export async function fetchIdentity(): Promise<GitIdentity> {
  if (store.demo) return mockIdentity
  return call<GitIdentity>('GET', '/api/git/identity')
}

export async function fetchRepos(): Promise<GitRepo[]> {
  if (store.demo) return mockRepos
  return (await call<{ repos: GitRepo[] }>('GET', '/api/git/repos')).repos
}

export async function fetchLocalRepos(): Promise<LocalRepo[]> {
  if (store.demo) return mockLocal
  return (await call<{ repos: LocalRepo[] }>('GET', '/api/git/local')).repos ?? []
}

// ---------- 角色分配与设置 ----------

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
