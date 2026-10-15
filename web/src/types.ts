// 与后端 / docs/DESIGN.md 保持一致的数据模型

export type RoleId = 'lead' | 'developer' | 'frontend' | 'tester' | 'reviewer'

export interface RoleInfo {
  id: RoleId
  name: string
  description: string
  strength: string
}

// 工具清单条目（界面可编辑的部分）
export interface ToolSpec {
  id: string
  name: string
  vendor: string
  commands: string[]
  paths?: Record<string, string[]>
  versionArgs?: string[]
  headless?: string[]
  usageArgs?: string[] // 有值表示能输出精确的 token 用量
  autoArgs?: string[] // 无人值守：免确认、限定在工作目录内修改
  streamArgs?: string[] // 输出事件流（实时查看过程），设置后代替 usageArgs
  readonlyArgs?: string[] // 只读模式，用于规划与评审
  authErrors?: string[] // 输出中表示"未登录"的关键字
  limitErrors?: string[] // 输出中表示"限流 / 额度用完"的关键字
  capability: number
  strengths: string[]
  disabled: boolean
}

export interface ToolDetection extends ToolSpec {
  source: 'builtin' | 'modified' | 'custom'
  installed: boolean
  path?: string
  version?: string
  error?: string
}

export interface Assignment {
  toolId: string
  toolName: string
  roles: RoleId[]
}

// 运行设置
export interface Settings {
  concurrency: number // 同时运行的工具进程数，默认 3，最多 5
  autoMerge: boolean // 测试通过后由项目管理直接合并到主分支
  maxAttempts?: number // 每个任务最多尝试次数
  idleMinutes?: number // 连续无输出多久判定为卡住
  timeoutMinutes?: number // 单次尝试最长时间
  paused?: boolean // 暂停派发
}

export interface TokenUsage {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
}

// 一次工具调用的计量记录：每次派发都会产生一条
export interface UsageRecord {
  id: string
  projectId: string
  taskId: string
  tool: string
  role: RoleId
  at: string
  durationSec: number
  usage: TokenUsage
  estimated: boolean // 工具不输出用量时按字符数估算
}

export type TaskStatus =
  | 'planned' // 已规划，等待依赖
  | 'ready' // 依赖已满足，可领取
  | 'running' // 开发中
  | 'testing' // 测试中
  | 'review' // 待评审/待集成
  | 'integrating' // 集成中
  | 'merged' // 已合并到集成分支
  | 'blocked' // 受阻：重试用完
  | 'waiting-user' // 等你处理
  | 'cancelled' // 已取消

export interface Task {
  id: string
  title: string
  kind: 'feature' | 'ui' | 'test' | 'infra'
  role: RoleId
  assignee: string // tool id
  status: TaskStatus
  dependsOn: string[]
  branch: string
  progress: number // 0-100，只表示阶段
  commits: number
  description?: string
  scope?: string[]
  acceptance?: string[]
  verify?: string[]
  attempts?: number
  lastError?: string
  waiting?: string // 等你处理时的说明
}

// 新建 / 修改任务时提交的字段
export interface TaskInput {
  title: string
  description?: string
  kind: Task['kind']
  role: RoleId
  assignee?: string
  dependsOn: string[]
  scope: string[]
  acceptance: string[]
  verify: string[]
}

export interface Report {
  id: string
  taskId: string
  from: string // tool id
  at: string
  status: TaskStatus
  summary: string
  commit?: string
  attempt?: number
  verify?: string
  tokens?: number
  files?: number
  added?: number
  removed?: number
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
  testCommand?: string
  setupCommand?: string
  deliverable?: boolean // 全部任务已合并，可以交付到主分支
  deliverNote?: string
}

// 添加项目
export interface CreateProjectRequest {
  source: 'github' | 'url' | 'local' | 'new'
  repo?: string
  url?: string
  path: string
  name?: string
  baseBranch?: string
  createRemote?: boolean
  description?: string
  testCommand?: string
}

// 一次尝试的状态栏（GET /api/runs）
export interface RunStat {
  projectId: string
  taskId: string
  tool: string
  role: RoleId
  attempt: number
  startedAt: number
  lastActivity: number
  phase: 'running' | 'verifying' | 'integrating' | 'done' | 'failed' | 'stopped' | 'waiting'
  files: string[]
  added: number
  removed: number
  tokens: number
  exit?: number
  result?: string
}

// 本机 git / GitHub 身份
export interface GitIdentity {
  gitName: string
  gitEmail: string
  gitVersion: string
  ghInstalled: boolean
  ghLoggedIn: boolean
  ghLogin?: string
  ghHost?: string
}

export interface GitRepo {
  nameWithOwner: string
  description: string
  isPrivate: boolean
  updatedAt: string
  url: string
  defaultBranch: string
}

export interface LocalRepo {
  name: string
  path: string
  remote?: string
  branch?: string
}

// 正在运行的工具进程（占用一个运行槽）
export interface RunningTask {
  tool: string
  projectId: string
  taskId: string
  role: RoleId
  startedAt: string
}
