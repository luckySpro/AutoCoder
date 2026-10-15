import { reactive } from 'vue'
import { mockAssignments, mockProjects, mockRoles, mockTools } from './mock'
import type { Assignment, Project, RoleId, RoleInfo, ToolDetection } from './types'

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
})

const ASSIGN_KEY = 'autocoder.assignments'

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
    store.assignments = loadSaved() ?? r.assignments ?? []
    store.demo = false
  } catch (e) {
    store.demo = true
    store.error = (e as Error).message
    store.tools = mockTools
    store.scannedAt = new Date().toISOString()
    store.manifestPath = '~/.autocoder/tools.yaml'
    store.roles = mockRoles
    store.assignments = loadSaved() ?? structuredClone(mockAssignments)
  } finally {
    store.loading = false
  }
}

function loadSaved(): Assignment[] | null {
  try {
    const raw = localStorage.getItem(ASSIGN_KEY)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

export function saveAssignments() {
  try {
    localStorage.setItem(ASSIGN_KEY, JSON.stringify(store.assignments))
  } catch {
    /* 隐私模式下忽略 */
  }
}

export function resetAssignments() {
  try {
    localStorage.removeItem(ASSIGN_KEY)
  } catch {
    /* ignore */
  }
  return scanTools()
}

export function toolName(id: string) {
  return store.tools.find((t) => t.id === id)?.name ?? id
}

export function roleName(id: RoleId) {
  return store.roles.find((r) => r.id === id)?.name ?? id
}
