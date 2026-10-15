// 演示数据：项目/任务/汇报暂未接后端；工具检测在后端不可用时也回落到这里。
import type { Assignment, Project, RoleInfo, ToolDetection } from './types'

export const mockRoles: RoleInfo[] = [
  { id: 'lead', name: '架构与项目管理', description: '负责架构设计、功能与视觉拆分、任务派发、进度汇总与分支集成', strength: 'architecture' },
  { id: 'developer', name: '开发', description: '认领功能任务，在独立分支与工作区中实现并提交', strength: 'coding' },
  { id: 'frontend', name: '前端/视觉', description: '实现界面、交互与视觉稿对应的任务', strength: 'frontend' },
  { id: 'tester', name: '测试', description: '为任务编写测试、执行回归，并向项目管理汇报质量结果', strength: 'testing' },
  { id: 'reviewer', name: '评审', description: '合并前审查分支差异，提出修改意见', strength: 'review' },
]

const t = (
  id: string, name: string, vendor: string, kind: 'cli' | 'ide', capability: number,
  strengths: string[], installed: boolean, version?: string, path?: string,
): ToolDetection => ({
  id, name, vendor, kind, capability, strengths, installed, version, path,
  commands: [id], headless: kind === 'cli' ? ['-p', '{prompt}'] : undefined,
})

export const mockTools: ToolDetection[] = [
  t('claude-code', 'Claude Code', 'Anthropic', 'cli', 95, ['architecture', 'coding', 'review', 'testing'], true, '2.0.14', '/usr/local/bin/claude'),
  t('codex', 'Codex CLI', 'OpenAI', 'cli', 90, ['coding', 'architecture', 'testing'], true, '0.46.0', '/opt/homebrew/bin/codex'),
  t('cursor', 'Cursor IDE', 'Anysphere', 'ide', 80, ['frontend', 'coding'], true, undefined, '/Applications/Cursor.app'),
  t('cursor-agent', 'Cursor Agent CLI', 'Anysphere', 'cli', 85, ['coding', 'frontend'], false),
  t('gemini-cli', 'Gemini CLI', 'Google', 'cli', 85, ['coding', 'review', 'frontend'], false),
  t('qwen-code', 'Qwen Code', 'Alibaba', 'cli', 75, ['coding', 'testing'], false),
  t('aider', 'Aider', 'Aider', 'cli', 70, ['coding', 'testing'], false),
]

export const mockAssignments: Assignment[] = [
  { toolId: 'claude-code', toolName: 'Claude Code', roles: ['lead', 'reviewer'], manual: false },
  { toolId: 'codex', toolName: 'Codex CLI', roles: ['developer', 'tester'], manual: false },
  { toolId: 'cursor', toolName: 'Cursor IDE', roles: ['frontend'], manual: true },
]

export const mockProjects: Project[] = [
  {
    id: 'shop-admin',
    name: 'shop-admin',
    path: '~/code/shop-admin',
    remote: 'git@github.com:lucky/shop-admin.git',
    baseBranch: 'main',
    integrationBranch: 'ac/integration',
    description: '商城管理后台：商品、订单、会员三大模块',
    updatedAt: '2026-10-15 08:42',
    tasks: [
      { id: 'T1', title: '架构设计与目录骨架', kind: 'infra', role: 'lead', assignee: 'claude-code', status: 'merged', dependsOn: [], branch: 'ac/T1-skeleton', progress: 100, commits: 3 },
      { id: 'T2', title: '商品 API（增删改查）', kind: 'feature', role: 'developer', assignee: 'codex', status: 'merged', dependsOn: ['T1'], branch: 'ac/T2-product-api', progress: 100, commits: 5 },
      { id: 'T3', title: '订单 API 与状态机', kind: 'feature', role: 'developer', assignee: 'codex', status: 'running', dependsOn: ['T1'], branch: 'ac/T3-order-api', progress: 60, commits: 4 },
      { id: 'T4', title: '后台布局与设计令牌', kind: 'ui', role: 'frontend', assignee: 'cursor', status: 'review', dependsOn: ['T1'], branch: 'ac/T4-layout', progress: 100, commits: 2 },
      { id: 'T5', title: '商品列表与编辑页', kind: 'ui', role: 'frontend', assignee: 'cursor', status: 'ready', dependsOn: ['T2', 'T4'], branch: 'ac/T5-product-ui', progress: 0, commits: 0 },
      { id: 'T6', title: '商品 API 集成测试', kind: 'test', role: 'tester', assignee: 'codex', status: 'testing', dependsOn: ['T2'], branch: 'ac/T6-product-test', progress: 70, commits: 2 },
      { id: 'T7', title: '订单页面', kind: 'ui', role: 'frontend', assignee: 'cursor', status: 'planned', dependsOn: ['T3', 'T4'], branch: 'ac/T7-order-ui', progress: 0, commits: 0 },
      { id: 'T8', title: '端到端回归', kind: 'test', role: 'tester', assignee: 'codex', status: 'planned', dependsOn: ['T5', 'T7'], branch: 'ac/T8-e2e', progress: 0, commits: 0 },
    ],
    reports: [
      { id: 'r9', taskId: 'T6', from: 'codex', at: '08:42', status: 'testing', summary: '12 个用例中 11 个通过；分页越界返回 500，已在 T2 分支上提出修复建议', commit: 'a41c9e2' },
      { id: 'r8', taskId: 'T4', from: 'cursor', at: '08:30', status: 'review', summary: '布局与深浅主题令牌完成，等待评审', commit: '7d02b11' },
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
      { id: 'T2', title: '首页与案例页', kind: 'ui', role: 'frontend', assignee: 'cursor', status: 'blocked', dependsOn: ['T1'], branch: 'ac/T2-home', progress: 40, commits: 2 },
    ],
    reports: [
      { id: 'r2', taskId: 'T2', from: 'cursor', at: '21:05', status: 'blocked', summary: '缺少案例图片素材，等待用户提供' },
      { id: 'r1', taskId: 'T1', from: 'claude-code', at: '20:30', status: 'merged', summary: '拆分为 2 个任务' },
    ],
  },
]
