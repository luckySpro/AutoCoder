// 与后端 / docs/DESIGN.md 保持一致的数据模型

export type RoleId = 'lead' | 'developer' | 'frontend' | 'tester' | 'reviewer'

export interface RoleInfo {
  id: RoleId
  name: string
  description: string
  strength: string
}

export interface ToolDetection {
  id: string
  name: string
  vendor: string
  kind: 'cli' | 'ide'
  commands: string[]
  headless?: string[]
  capability: number
  strengths: string[]
  installed: boolean
  path?: string
  version?: string
  error?: string
}

export interface Assignment {
  toolId: string
  toolName: string
  roles: RoleId[]
  manual: boolean
}

export type TaskStatus =
  | 'planned' // 已规划，等待依赖
  | 'ready' // 依赖已满足，可领取
  | 'running' // 开发中
  | 'testing' // 测试中
  | 'review' // 待评审/待集成
  | 'merged' // 已合并到集成分支
  | 'blocked' // 受阻，需要项目管理处理

export interface Task {
  id: string
  title: string
  kind: 'feature' | 'ui' | 'test' | 'infra'
  role: RoleId
  assignee: string // tool id
  status: TaskStatus
  dependsOn: string[]
  branch: string
  progress: number // 0-100
  commits: number
}

export interface Report {
  id: string
  taskId: string
  from: string // tool id
  at: string
  status: TaskStatus
  summary: string
  commit?: string
}

export interface Project {
  id: string
  name: string
  path: string
  remote?: string
  baseBranch: string
  integrationBranch: string
  description: string
  updatedAt: string
  tasks: Task[]
  reports: Report[]
}
