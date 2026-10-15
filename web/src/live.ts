// 运行中 CLI 进程的实时输出。
// 真实服务（M2）通过 SSE `GET /api/runs/{run}/stream` 推送，断线时带上最后的序号续传（见 DESIGN §6.4）。
// 目前没有真实进程，这里按脚本模拟输出流，界面与真实数据使用同一套结构。
import { reactive, ref } from 'vue'
import { store } from './api'
import type { RunningTask } from './types'

export type ActKind = 'say' | 'read' | 'edit' | 'run' | 'usage' | 'warn'
export type Tone = 'ok' | 'bad' | 'dim' | 'cmd' | 'warn'

// 动作视图的一条记录（从事件流解析）
export interface LiveAct { seq: number; at: number; kind: ActKind; text: string; meta?: string; tone?: Tone }
// 原始输出的一行
export interface LiveRaw { seq: number; at: number; text: string; tone?: Tone; err?: boolean }

export interface LiveRun {
  taskId: string
  projectId: string
  tool: string
  attempt: number
  startedAt: number
  lastActivity: number
  acts: LiveAct[]
  raw: LiveRaw[]
  files: string[]
  added: number
  removed: number
  tokens: number
  state: 'running' | 'stopped'
}

interface Step { kind: ActKind; text: string; meta?: string; tone?: Tone; raw: (string | [string, Tone] | [string, Tone, 'err'])[]; file?: string; add?: number; del?: number; tok?: number; gap: number }

const scripts: Record<string, Step[]> = {
  codex: [
    { kind: 'say', text: '读取任务说明：订单状态机需要支持退款；上次尝试失败原因是退款分支缺少测试。', raw: [['codex · 第 2 次尝试 · workspace-write', 'dim'], '读取任务说明与上次的失败原因…'], gap: 2 },
    { kind: 'read', text: 'internal/order/state.go', raw: [['▸ read internal/order/state.go (184 行)', 'dim']], gap: 2 },
    { kind: 'read', text: 'internal/order/state_test.go', raw: [['▸ read internal/order/state_test.go (96 行)', 'dim']], gap: 2 },
    { kind: 'edit', text: 'internal/order/state.go', meta: '+18 −4', file: 'internal/order/state.go', add: 18, del: 4, raw: [['✎ apply_patch internal/order/state.go', 'cmd'], ['  +18 −4', 'dim']], gap: 4 },
    { kind: 'run', text: 'go test ./internal/order/...', meta: '1 个失败', tone: 'bad', raw: [['$ go test ./internal/order/...', 'cmd'], '--- FAIL: TestRefundFromShipped (0.00s)', ['    state_test.go:88: 已发货的订单不应允许直接退款，得到 refunded', 'bad', 'err'], ['FAIL\tshop-admin/internal/order\t0.418s', 'bad', 'err']], gap: 5 },
    { kind: 'usage', text: '本轮用量', meta: '+3.1k token', tok: 3100, raw: [['tokens: in 2,418 · out 702', 'dim']], gap: 1 },
    { kind: 'say', text: '已发货的订单要先进入"退货中"，收到退货后才能退款。补上这一步转换。', raw: ['已发货的订单要先进入"退货中"，收到退货后才能退款。补上这一步转换。'], gap: 3 },
    { kind: 'edit', text: 'internal/order/state.go', meta: '+9 −2', file: 'internal/order/state.go', add: 9, del: 2, raw: [['✎ apply_patch internal/order/state.go', 'cmd'], ['  +9 −2', 'dim']], gap: 3 },
    { kind: 'edit', text: 'internal/order/refund.go', meta: '新文件 +42', file: 'internal/order/refund.go', add: 42, raw: [['✎ add internal/order/refund.go', 'cmd'], ['  +42', 'dim']], gap: 4 },
    { kind: 'run', text: 'go test ./internal/order/...', meta: '14 通过', tone: 'ok', raw: [['$ go test ./internal/order/...', 'cmd'], ['ok  \tshop-admin/internal/order\t0.402s', 'ok']], gap: 5 },
    { kind: 'usage', text: '本轮用量', meta: '+4.6k token', tok: 4600, raw: [['tokens: in 3,880 · out 731', 'dim']], gap: 1 },
    { kind: 'say', text: '状态机与退款流程完成，测试 14 项通过。还需要在 API 层暴露退款接口。', raw: ['状态机与退款流程完成，测试 14 项通过。还需要在 API 层暴露退款接口。'], gap: 4 },
  ],
  'claude-code': [
    { kind: 'say', text: '评审 T4：对照验收标准检查后台布局与设计令牌的改动。', raw: [['claude · 只读评审 · 第 1 次尝试', 'dim'], '评审 T4：对照验收标准检查后台布局与设计令牌的改动。'], gap: 2 },
    { kind: 'run', text: 'git diff ac/integration...ac/T4-layout --stat', meta: '6 个文件', raw: [['$ git diff ac/integration...ac/T4-layout --stat', 'cmd'], ' web/src/styles/tokens.css       |  96 ++++++', ' web/src/layouts/AdminLayout.vue | 148 ++++++++', ' 6 files changed, 301 insertions(+), 12 deletions(-)'], gap: 3 },
    { kind: 'read', text: 'web/src/styles/tokens.css', raw: [['▸ Read web/src/styles/tokens.css', 'dim']], gap: 3 },
    { kind: 'read', text: 'web/src/layouts/AdminLayout.vue', raw: [['▸ Read web/src/layouts/AdminLayout.vue', 'dim']], gap: 2 },
    { kind: 'usage', text: '本轮用量', meta: '+5.8k token', tok: 5800, raw: [['usage: input 5,102 · output 664 · cache read 12,880', 'dim']], gap: 1 },
    { kind: 'warn', text: '发现问题：暗色主题下侧栏选中项对比度不足（验收第 3 条）。', tone: 'warn', raw: [['⚠ 验收第 3 条未满足：暗色主题下侧栏选中项对比度只有 2.8:1', 'warn']], gap: 3 },
    { kind: 'run', text: 'npm --prefix web run type-check', meta: '通过', tone: 'ok', raw: [['$ npm --prefix web run type-check', 'cmd'], ['> vue-tsc --noEmit', 'dim'], ['done in 6.1s', 'ok']], gap: 5 },
    { kind: 'say', text: '结论：需要修改。1）暗色侧栏选中项提高对比度；2）窄屏下侧栏没有收起按钮。', raw: ['结论：需要修改', '1. 暗色主题侧栏选中项的对比度提高到 4.5:1 以上', '2. 窄屏下侧栏缺少收起按钮'], gap: 6 },
  ],
}

let seq = 0
export const now = ref(Date.now())
export const runs = reactive(new Map<string, LiveRun>())
const cursor = new Map<string, number>()
// 运行按 项目/任务 区分，不同项目可能有同名任务
export const runKey = (projectId: string, taskId: string) => `${projectId}/${taskId}`
let timer: number | undefined

function apply(run: LiveRun, step: Step, at: number) {
  run.acts.push({ seq: ++seq, at, kind: step.kind, text: step.text, meta: step.meta, tone: step.tone })
  for (const r of step.raw) {
    const [text, tone, err] = typeof r === 'string' ? [r] : r
    run.raw.push({ seq: ++seq, at, text, tone, err: err === 'err' })
  }
  if (step.file && !run.files.includes(step.file)) run.files.push(step.file)
  run.added += step.add ?? 0
  run.removed += step.del ?? 0
  run.tokens += step.tok ?? 0
  run.lastActivity = at
  if (run.acts.length > 300) run.acts.splice(0, run.acts.length - 300)
  if (run.raw.length > 1000) run.raw.splice(0, run.raw.length - 1000)
}

function create(r: RunningTask): LiveRun {
  const script = scripts[r.tool] ?? scripts.codex
  const run: LiveRun = {
    taskId: r.taskId, projectId: r.projectId, tool: r.tool, attempt: r.tool === 'codex' ? 2 : 1,
    startedAt: Date.now() - (r.tool === 'codex' ? 7 : 11) * 60_000, lastActivity: Date.now(),
    acts: [], raw: [], files: [], added: 0, removed: 0, tokens: 0, state: 'running',
  }
  // 打开时先补发已经发生的一段输出
  const pre = Math.min(5, script.length)
  for (let i = 0; i < pre; i++) apply(run, script[i], Date.now() - (pre - i) * 20_000)
  cursor.set(runKey(r.projectId, r.taskId), pre)
  return run
}

function tick() {
  now.value = Date.now()
  for (const run of runs.values()) {
    if (run.state !== 'running') continue
    const script = scripts[run.tool] ?? scripts.codex
    const key = runKey(run.projectId, run.taskId)
    const i = cursor.get(key) ?? 0
    const step = script[i % script.length]
    if (now.value - run.lastActivity < step.gap * 1000) continue
    apply(run, step, now.value)
    cursor.set(key, i + 1)
  }
}

export function startLive() {
  for (const r of store.running) {
    const key = runKey(r.projectId, r.taskId)
    if (!runs.has(key)) runs.set(key, create(r))
  }
  if (!timer) timer = window.setInterval(tick, 1000)
}

export function stopRun(key: string) {
  const run = runs.get(key)
  if (!run || run.state !== 'running') return
  run.state = 'stopped'
  const at = Date.now()
  run.acts.push({ seq: ++seq, at, kind: 'warn', text: '已停止：进程组已结束，改动作为 WIP 保留在任务分支上。', tone: 'warn' })
  run.raw.push({ seq: ++seq, at, text: '^C 已由 AutoCoder 停止（SIGTERM → 进程组）', tone: 'warn' })
}

export function fmtDuration(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000))
  const m = Math.floor(s / 60)
  return m >= 60 ? `${Math.floor(m / 60)}:${String(m % 60).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}` : `${m}:${String(s % 60).padStart(2, '0')}`
}

export function clock(at: number) {
  const d = new Date(at)
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, '0')).join(':')
}
