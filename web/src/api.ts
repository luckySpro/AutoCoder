import { reactive } from 'vue'
import { mockAssignments, mockIdentity, mockLocal, mockProjects, mockRepos, mockRoles, mockRunning, mockTools, mockUsage } from './mock'
import type {
  Assignment, CreateProjectRequest, GitIdentity, GitRepo, LocalRepo, Project, Report, RoleId, RoleInfo, RunningTask, RunStat, Settings,
  Task, TaskInput, ToolDetection, ToolSpec, UsageRecord,
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
  projects: [] as Project[],
  usage: [] as UsageRecord[],
  running: [] as RunningTask[],
  runStats: [] as RunStat[], // 真实服务：每个任务最近一次尝试
  toolState: {} as Record<string, string>, // 工具冷却 / 等待登录
  loadErrors: [] as string[],
  settings: loadJSON<Settings>(SETTINGS_KEY) ?? { ...DEFAULT_SETTINGS },
})

type ToolsResp = { tools: ToolDetection[]; scannedAt: string; manifestPath: string }

function applyTools(t: ToolsResp) {
  store.tools = t.tools
  store.scannedAt = t.scannedAt
  store.manifestPath = t.manifestPath
}

export async function scanTools(rescan = false) {
  store.loading = true
  try {
    applyTools(await call<ToolsResp>('GET', `/api/tools${rescan ? '?refresh=1' : ''}`))
    const r = await call<{ roles: RoleInfo[]; assignments: Assignment[] | null }>('GET', '/api/roles')
    store.roles = r.roles
    store.assignments = r.assignments ?? []
    if (store.demo || !realLoaded) {
      store.demo = false
      await loadSettings().catch(() => {})
      await refresh()
      startPolling()
    }
  } catch {
    store.demo = true
    if (!store.tools.length) store.tools = structuredClone(mockTools)
    store.scannedAt = new Date().toISOString()
    store.manifestPath = '~/.autocoder/tools.yaml'
    store.roles = mockRoles
    store.assignments = loadJSON<Assignment[]>(ASSIGN_KEY) ?? structuredClone(mockAssignments)
    if (!realLoaded && !store.projects.length) {
      store.projects = structuredClone(mockProjects)
      store.usage = mockUsage
      store.running = structuredClone(mockRunning)
    }
  } finally {
    store.loading = false
  }
}

// ---------- 工具清单管理 ----------

export function specOf(t: ToolDetection): ToolSpec {
  const { id, name, vendor, commands, paths, versionArgs, headless, usageArgs, autoArgs, streamArgs, readonlyArgs, authErrors, limitErrors, capability, strengths, disabled } = t
  return { id, name, vendor, commands, paths, versionArgs, headless, usageArgs, autoArgs, streamArgs, readonlyArgs, authErrors, limitErrors, capability, strengths, disabled }
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

// ---------- 角色分配与设置（真实服务保存在 ~/.autocoder/settings.json）----------

async function loadSettings() {
  const r = await call<{ settings: Settings & { assignments?: Assignment[] }; assignments: Assignment[] }>('GET', '/api/settings')
  const { assignments: _saved, ...rest } = r.settings
  store.settings = rest
  store.assignments = r.assignments ?? []
}

export async function saveAssignments(assignments = store.assignments) {
  if (store.demo) {
    saveJSON(ASSIGN_KEY, store.assignments)
    saveJSON(SETTINGS_KEY, store.settings)
    return
  }
  const r = await call<{ settings: Settings; assignments: Assignment[] }>('PUT', '/api/settings', { ...store.settings, assignments })
  const { assignments: _saved, ...rest } = r.settings as Settings & { assignments?: Assignment[] }
  store.settings = rest
  store.assignments = r.assignments
}

export async function resetAssignments() {
  try {
    localStorage.removeItem(ASSIGN_KEY)
    localStorage.removeItem(SETTINGS_KEY)
  } catch {
    /* ignore */
  }
  store.settings = { ...DEFAULT_SETTINGS }
  if (!store.demo) await saveAssignments([])
  return scanTools()
}

// ---------- 项目、任务、运行、计量（真实服务）----------

let realLoaded = false
let pollTimer: number | undefined

const pad = (n: number) => String(n).padStart(2, '0')
export const mmdd = (d: Date) => `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
const hhmm = (d: Date) => `${pad(d.getHours())}:${pad(d.getMinutes())}`
// 今天只显示时间，其他日子带上日期
export function fmtAt(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return mmdd(d) === mmdd(new Date()) ? hhmm(d) : `${mmdd(d)} ${hhmm(d)}`
}
export const todayKey = () => (store.demo ? '10-15' : mmdd(new Date()))

type ProjectsResp = { projects: (Project & { reports: Report[] })[]; errors?: string[] }

export async function refresh() {
  if (store.demo) return
  const [p, r, u] = await Promise.all([
    call<ProjectsResp>('GET', '/api/projects'),
    call<{ runs: RunStat[]; tools: Record<string, string> }>('GET', '/api/runs'),
    call<{ records: UsageRecord[] }>('GET', '/api/usage'),
  ])
  store.projects = p.projects.map((x) => ({
    ...x,
    updatedAt: fmtAt(x.updatedAt),
    remote: x.remote || undefined,
    tasks: x.tasks.map((t) => ({ ...t, dependsOn: t.dependsOn ?? [], assignee: t.assignee ?? '' })),
    reports: (x.reports ?? []).map((rp) => ({ ...rp, at: fmtAt(rp.at) })),
  }))
  store.loadErrors = p.errors ?? []
  store.runStats = r.runs
  store.toolState = r.tools ?? {}
  store.running = r.runs
    .filter((x) => x.phase === 'running' || x.phase === 'verifying' || x.phase === 'integrating')
    .map((x) => ({ tool: x.tool, projectId: x.projectId, taskId: x.taskId, role: x.role, startedAt: hhmm(new Date(x.startedAt)) }))
  store.usage = u.records.map((x) => ({ ...x, at: `${mmdd(new Date(x.at))} ${hhmm(new Date(x.at))}` }))
  realLoaded = true
}

function startPolling() {
  if (pollTimer) return
  pollTimer = window.setInterval(() => {
    if (document.visibilityState === 'visible') refresh().catch(() => {})
  }, 2500)
}

async function mutate<T>(method: string, url: string, body?: unknown): Promise<T> {
  const r = await call<T>(method, url, body)
  await refresh().catch(() => {})
  return r
}

const pid = (id: string) => encodeURIComponent(id)

export async function createProject(req: CreateProjectRequest): Promise<{ id: string; warnings: string[] }> {
  if (store.demo) throw new ApiError('预览模式不能添加真实项目', 0)
  const r = await mutate<{ project: Project; warnings: string[] | null }>('POST', '/api/projects', req)
  return { id: r.project.id, warnings: r.warnings ?? [] }
}

export function updateProject(id: string, patch: Partial<Pick<Project, 'description' | 'testCommand' | 'setupCommand'>>) {
  return mutate<Project>('PATCH', `/api/projects/${pid(id)}`, patch)
}

export function removeProject(id: string) {
  return mutate('DELETE', `/api/projects/${pid(id)}`)
}

export async function createTask(projectId: string, input: TaskInput): Promise<Task> {
  if (store.demo) {
    const p = store.projects.find((x) => x.id === projectId)!
    const id = `T${p.tasks.reduce((n, t) => Math.max(n, Number(t.id.replace(/\D/g, '')) || 0), 0) + 1}`
    const t: Task = {
      ...input, id, assignee: input.assignee ?? '', status: input.dependsOn.length ? 'planned' : 'ready',
      branch: `ac/${id}-task`, progress: 0, commits: 0, attempts: 0,
    }
    p.tasks.push(t)
    return t
  }
  return mutate<Task>('POST', `/api/projects/${pid(projectId)}/tasks`, input)
}

export async function updateTask(projectId: string, taskId: string, input: Partial<TaskInput>) {
  if (store.demo) {
    const t = store.projects.find((x) => x.id === projectId)?.tasks.find((x) => x.id === taskId)
    if (t) Object.assign(t, input)
    return
  }
  return mutate<Task>('PATCH', `/api/projects/${pid(projectId)}/tasks/${pid(taskId)}`, input)
}

export async function taskAction(projectId: string, taskId: string, action: 'retry' | 'cancel' | 'approve') {
  if (store.demo) throw new ApiError('预览模式不能派发任务，请在本机运行 AutoCoder', 0)
  return mutate('POST', `/api/projects/${pid(projectId)}/tasks/${pid(taskId)}/${action}`)
}

export async function deliverProject(projectId: string): Promise<{ status: string; detail?: string; commit?: string; note?: string }> {
  if (store.demo) throw new ApiError('预览模式不能交付，请在本机运行 AutoCoder', 0)
  return mutate('POST', `/api/projects/${pid(projectId)}/deliver`)
}

export async function stopRemoteRun(projectId: string, taskId: string) {
  return mutate('POST', `/api/runs/${pid(projectId)}/${pid(taskId)}/stop`)
}

export const logURL = (projectId: string, taskId: string, attempt: number) =>
  `/api/runs/${pid(projectId)}/${pid(taskId)}/log?attempt=${attempt}`

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
