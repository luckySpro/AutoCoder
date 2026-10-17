// 演示数据：项目/任务/汇报暂未接后端；工具检测在后端不可用时也回落到这里。
import type { Assignment, GitIdentity, GitRepo, LocalRepo, Project, RoleId, RoleInfo, RunningTask, ToolDetection, UsageRecord } from './types'

export const mockRoles: RoleInfo[] = [
  { id: 'lead', name: '架构与项目管理', description: '负责架构设计、功能与视觉拆分、任务派发、进度汇总与分支集成', strength: 'architecture' },
  { id: 'developer', name: '开发', description: '认领功能任务，在独立分支与工作区中实现并提交', strength: 'coding' },
  { id: 'frontend', name: '前端/视觉', description: '实现界面、交互与视觉稿对应的任务', strength: 'frontend' },
  { id: 'tester', name: '测试', description: '为任务编写测试、执行回归，并向项目管理汇报质量结果', strength: 'testing' },
  { id: 'reviewer', name: '评审', description: '合并前审查分支差异，提出修改意见', strength: 'review' },
]

const t = (
  id: string, name: string, vendor: string, capability: number, strengths: string[],
  installed: boolean, exact: boolean, version?: string, path?: string,
): ToolDetection => ({
  id, name, vendor, capability, strengths, installed, version, path, source: 'builtin', disabled: false,
  commands: [id === 'claude-code' ? 'claude' : id], versionArgs: ['--version'],
  headless: ['-p', '{prompt}'], usageArgs: exact ? ['--output-format', 'json'] : undefined,
})

export const mockTools: ToolDetection[] = [
  t('claude-code', 'Claude Code', 'Anthropic', 95, ['architecture', 'coding', 'review', 'testing'], true, true, '2.0.14', '/usr/local/bin/claude'),
  t('codex', 'Codex CLI', 'OpenAI', 90, ['coding', 'architecture', 'testing'], true, true, '0.46.0', '/opt/homebrew/bin/codex'),
  t('cursor-agent', 'Cursor Agent CLI', 'Anysphere', 85, ['coding', 'frontend'], true, true, '2025.10.02', '/Users/lucky/.local/bin/cursor-agent'),
  t('gemini-cli', 'Gemini CLI', 'Google', 85, ['coding', 'review', 'frontend'], false, true),
  t('qwen-code', 'Qwen Code', 'Alibaba', 75, ['coding', 'testing'], false, true),
  t('copilot-cli', 'GitHub Copilot CLI', 'GitHub', 78, ['coding', 'review'], false, false),
  t('opencode', 'OpenCode', 'SST', 72, ['coding', 'testing'], false, false),
  t('aider', 'Aider', 'Aider', 70, ['coding', 'testing'], false, false),
]

export const mockAssignments: Assignment[] = [
  { toolId: 'claude-code', toolName: 'Claude Code', roles: ['lead', 'reviewer'] },
  { toolId: 'codex', toolName: 'Codex CLI', roles: ['developer', 'tester'] },
  { toolId: 'cursor-agent', toolName: 'Cursor Agent CLI', roles: ['frontend'] },
]

export const mockProjects: Project[] = [
  {
    id: 'shop-admin',
    name: 'shop-admin',
    path: '~/code/shop-admin',
    remote: 'git@github.com:lucky/shop-admin.git',
    baseBranch: 'main',
    integrationBranch: 'ac/integration',
    testCommand: 'go test ./...',
    description: '商城管理后台：商品、订单、会员三大模块',
    updatedAt: '2026-10-15 08:42',
    tasks: [
      { id: 'T1', title: '架构设计与目录骨架', kind: 'infra', role: 'lead', assignee: 'claude-code', status: 'merged', dependsOn: [], branch: 'ac/T1-skeleton', progress: 100, commits: 3 },
      { id: 'T2', title: '商品 API（增删改查）', kind: 'feature', role: 'developer', assignee: 'codex', status: 'merged', dependsOn: ['T1'], branch: 'ac/T2-product-api', progress: 100, commits: 5 },
      { id: 'T3', title: '订单 API 与状态机', kind: 'feature', role: 'developer', assignee: 'codex', status: 'running', dependsOn: ['T1'], branch: 'ac/T3-order-api', progress: 60, commits: 4 },
      { id: 'T4', title: '后台布局与设计令牌', kind: 'ui', role: 'frontend', assignee: 'cursor-agent', status: 'review', dependsOn: ['T1'], branch: 'ac/T4-layout', progress: 100, commits: 2 },
      { id: 'T5', title: '商品列表与编辑页', kind: 'ui', role: 'frontend', assignee: 'cursor-agent', status: 'ready', dependsOn: ['T2', 'T4'], branch: 'ac/T5-product-ui', progress: 0, commits: 0 },
      { id: 'T6', title: '商品 API 集成测试', kind: 'test', role: 'tester', assignee: 'codex', status: 'review', dependsOn: ['T2'], branch: 'ac/T6-product-test', progress: 100, commits: 2 },
      { id: 'T7', title: '订单页面', kind: 'ui', role: 'frontend', assignee: 'cursor-agent', status: 'planned', dependsOn: ['T3', 'T4'], branch: 'ac/T7-order-ui', progress: 0, commits: 0 },
      { id: 'T8', title: '端到端回归', kind: 'test', role: 'tester', assignee: 'codex', status: 'planned', dependsOn: ['T5', 'T7'], branch: 'ac/T8-e2e', progress: 0, commits: 0 },
    ],
    tokenBudget: 3_000_000,
    spent: 1_284_000,
    requests: [
      {
        id: 'R2', status: 'proposed', tool: 'claude-code', attempts: 1, tokens: 41_200, createdAt: '08:31', updatedAt: '08:39',
        text: '加会员模块：会员列表、等级与积分；下单时按等级打折，积分可抵扣运费', qa: [], questions: [], errors: [], tasks: [],
        notes: ['P2 与 P3 的修改范围重叠（internal/member/** / internal/member/points.go），改为 P3 在 P2 合并后开始'],
        plan: {
          summary: '会员独立成 internal/member 包：Member、Level、PointsLedger 三张表。订单在结算时调用 member.Discount 与 member.RedeemShipping，接口在 P1 定好后 P2、P4 并行。\n假设：积分 100 分抵 1 元，只抵运费不抵商品金额。',
          testCommand: 'go test ./...',
          tasks: [
            { id: 'P1', title: '会员数据模型与迁移', kind: 'feature', role: 'developer', dependsOn: [], scope: ['internal/member/model.go', 'migrations/**'], acceptance: ['三张表的迁移可重复执行', '等级有默认数据'], verify: ['go test ./internal/member/...'] },
            { id: 'P2', title: '等级折扣与订单结算接入', kind: 'feature', role: 'developer', dependsOn: ['P1'], scope: ['internal/member/**', 'internal/order/checkout.go'], acceptance: ['金卡 9 折、银卡 95 折', '折扣写入订单明细'], verify: ['go test ./internal/member/... ./internal/order/...'] },
            { id: 'P3', title: '积分账本与运费抵扣', kind: 'feature', role: 'developer', dependsOn: ['P1', 'P2'], scope: ['internal/member/points.go'], acceptance: ['积分变动有流水', '抵扣不超过运费'], verify: ['go test ./internal/member/...'] },
            { id: 'P4', title: '会员列表与详情页', kind: 'ui', role: 'frontend', dependsOn: ['P1'], scope: ['web/src/views/member/**'], acceptance: ['列表可按等级筛选', '详情显示积分流水'], verify: ['npm --prefix web run type-check'] },
          ],
        },
      },
      {
        id: 'R1', status: 'approved', tool: 'claude-code', attempts: 1, tokens: 63_800, createdAt: '10-14 09:02', updatedAt: '10-14 09:20',
        text: '商城后台第一期：商品和订单的接口与页面，带集成测试和端到端回归', qa: [], questions: [], errors: [], notes: [],
        tasks: ['T1', 'T2', 'T3', 'T4', 'T5', 'T6', 'T7', 'T8'],
      },
    ],
    reports: [
      { id: 'r11', taskId: 'R2', from: 'claude-code', at: '08:39', status: 'proposed' as never, summary: '生成了计划：4 个任务' },
      { id: 'r10', taskId: 'T4', from: 'claude-code', at: '08:47', status: 'review', summary: '暗色主题下侧栏选中项对比度不足', review: 'changes', issues: ['web/src/styles/tokens.css：--nav-active 在暗色下对比度 2.8:1，需要 4.5:1 以上', '窄屏下侧栏缺少收起按钮'] },
      { id: 'r9', taskId: 'T6', from: 'codex', at: '08:42', status: 'review', summary: '12 个用例全部通过；顺手修复了分页越界返回 500 的问题，等待集成', commit: 'a41c9e2' },
      { id: 'r8', taskId: 'T4', from: 'cursor-agent', at: '08:30', status: 'review', summary: '布局与深浅主题令牌完成，等待评审', commit: '7d02b11' },
      { id: 'r7', taskId: 'T3', from: 'codex', at: '08:12', status: 'running', summary: '订单状态机（待支付→已支付→已发货→完成/取消）已实现，正在补退款分支', commit: 'e93f0a4' },
      { id: 'r6', taskId: 'T2', from: 'claude-code', at: '07:55', status: 'merged', summary: '评审通过，已合并到 ac/integration；集成分支测试全部通过', commit: '5bb7c30' },
      { id: 'r5', taskId: 'T1', from: 'claude-code', at: '07:10', status: 'merged', summary: '拆分为 8 个任务，生成依赖图并派发给 3 个工具' },
    ],
  },
  {
    id: 'landing',
    name: 'company-landing',
    path: '~/code/company-landing',
    baseBranch: 'main',
    integrationBranch: 'ac/integration',
    description: '企业官网改版',
    updatedAt: '2026-10-14 21:05',
    tasks: [
      { id: 'T1', title: '信息架构与视觉拆分', kind: 'infra', role: 'lead', assignee: 'claude-code', status: 'merged', dependsOn: [], branch: 'ac/T1-ia', progress: 100, commits: 1 },
      { id: 'T2', title: '首页与案例页', kind: 'ui', role: 'frontend', assignee: 'cursor-agent', status: 'blocked', dependsOn: ['T1'], branch: 'ac/T2-home', progress: 40, commits: 2 },
    ],
    reports: [
      { id: 'r2', taskId: 'T2', from: 'cursor-agent', at: '21:05', status: 'blocked', summary: '缺少案例图片素材，等待用户提供' },
      { id: 'r1', taskId: 'T1', from: 'claude-code', at: '20:30', status: 'merged', summary: '拆分为 2 个任务' },
    ],
  },
]

// 按任务进度生成确定性的计量记录：每次派发（开发、测试、评审、集成）都有一条
function seeded(seed: number) {
  return () => {
    seed = (seed * 9301 + 49297) % 233280
    return seed / 233280
  }
}

function buildUsage(): UsageRecord[] {
  const out: UsageRecord[] = []
  const rnd = seeded(42)
  const order = ['planned', 'ready', 'running', 'testing', 'review', 'merged', 'blocked']
  for (const p of mockProjects) {
    const lead = 'claude-code'
    let n = 0
    const push = (taskId: string, tool: string, role: RoleId, scale: number, hour: number) => {
      const input = Math.round((20_000 + rnd() * 60_000) * scale)
      out.push({
        id: `${p.id}-${++n}`, projectId: p.id, taskId, tool, role,
        at: `10-${p.id === 'landing' ? 14 : 15} ${String(hour).padStart(2, '0')}:${String(Math.floor(rnd() * 60)).padStart(2, '0')}`,
        durationSec: Math.round(60 + rnd() * 600 * scale),
        usage: {
          input, output: Math.round(input * (0.08 + rnd() * 0.12)),
          cacheRead: Math.round(input * (1.5 + rnd() * 3)), cacheWrite: Math.round(input * (0.1 + rnd() * 0.3)),
        },
        estimated: false,
      })
    }
    for (const task of p.tasks) {
      const stage = order.indexOf(task.status)
      const h = 7 + Math.floor(rnd() * 2)
      if (task.role === 'lead') { push(task.id, lead, 'lead', 2.5, h); continue }
      if (stage >= 2) push(task.id, task.assignee, task.role, 1, h)
      if (task.commits > 3) push(task.id, task.assignee, task.role, 0.6, h + 1)
      if (stage >= 3 && stage !== 6) push(task.id, 'codex', 'tester', 0.7, h + 1)
      if (stage >= 4 && stage !== 6) push(task.id, lead, 'reviewer', 0.4, h + 1)
      if (stage === 5) push(task.id, lead, 'lead', 0.3, h + 1)
    }
  }
  return out
}

export const mockUsage: UsageRecord[] = buildUsage()

export const mockRunning: RunningTask[] = [
  { tool: 'codex', projectId: 'shop-admin', taskId: 'T3', role: 'developer', startedAt: '08:51' },
  { tool: 'claude-code', projectId: 'shop-admin', taskId: 'T4', role: 'reviewer', startedAt: '08:47' },
]

export const mockIdentity: GitIdentity = {
  gitName: 'Lucky', gitEmail: 'lucky@example.com', gitVersion: '2.47.1',
  ghInstalled: true, ghLoggedIn: true, ghLogin: 'luckySpro', ghHost: 'github.com',
}

export const mockRepos: GitRepo[] = [
  { nameWithOwner: 'luckySpro/AutoCoder', description: '单机多 AI 开发工具协同', isPrivate: false, updatedAt: '2026-10-15T00:40:00Z', url: 'https://github.com/luckySpro/AutoCoder', defaultBranch: 'main' },
  { nameWithOwner: 'luckySpro/shop-admin', description: '商城管理后台', isPrivate: true, updatedAt: '2026-10-14T09:12:00Z', url: 'https://github.com/luckySpro/shop-admin', defaultBranch: 'main' },
  { nameWithOwner: 'luckySpro/freemind', description: '离线思维导图', isPrivate: true, updatedAt: '2026-10-11T16:30:00Z', url: 'https://github.com/luckySpro/freemind', defaultBranch: 'main' },
  { nameWithOwner: 'luckySpro/macntfs', description: 'macOS NTFS 读写工具', isPrivate: true, updatedAt: '2026-10-02T03:20:00Z', url: 'https://github.com/luckySpro/macntfs', defaultBranch: 'master' },
  { nameWithOwner: 'luckySpro/company-landing', description: '企业官网', isPrivate: false, updatedAt: '2026-09-28T11:05:00Z', url: 'https://github.com/luckySpro/company-landing', defaultBranch: 'main' },
  { nameWithOwner: 'luckySpro/dotfiles', description: '', isPrivate: false, updatedAt: '2026-08-19T07:44:00Z', url: 'https://github.com/luckySpro/dotfiles', defaultBranch: 'main' },
]

export const mockLocal: LocalRepo[] = [
  { name: 'shop-admin', path: '~/code/shop-admin', remote: 'git@github.com:luckySpro/shop-admin.git', branch: 'main' },
  { name: 'company-landing', path: '~/code/company-landing', remote: 'git@github.com:luckySpro/company-landing.git', branch: 'main' },
  { name: 'experiments', path: '~/code/experiments', branch: 'dev' },
]
