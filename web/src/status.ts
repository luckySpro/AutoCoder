import type { TaskStatus } from './types'

// tone 对应 .pill 的语义类
export const statusMeta: Record<TaskStatus, { label: string; tone: string }> = {
  planned: { label: '等待依赖', tone: '' },
  ready: { label: '可派发', tone: 'accent' },
  running: { label: '开发中', tone: 'run' },
  testing: { label: '测试中', tone: 'warn' },
  review: { label: '评审中', tone: 'accent' },
  integrating: { label: '集成中', tone: 'warn' },
  merged: { label: '已合并', tone: 'ok' },
  blocked: { label: '受阻', tone: 'bad' },
  'waiting-user': { label: '等你处理', tone: 'bad' },
  cancelled: { label: '已取消', tone: '' },
}

// 运行阶段
export const phaseMeta: Record<string, string> = {
  running: '运行中', verifying: '验证中', reviewing: '评审中', 'awaiting-review': '待评审', integrating: '集成中', planning: '规划中',
  done: '已完成', failed: '未通过', stopped: '已停止', waiting: '等你确认',
}

export const boardColumns: TaskStatus[] = ['planned', 'ready', 'running', 'testing', 'review', 'merged']

// 未知状态也能显示
export const sm = (s: string) => statusMeta[s as TaskStatus] ?? { label: s, tone: '' }

// 需求状态
export const requestMeta: Record<string, { label: string; tone: string }> = {
  planning: { label: '规划中', tone: 'run' },
  questions: { label: '等你回答', tone: 'bad' },
  proposed: { label: '等你确认', tone: 'accent' },
  approved: { label: '已确认', tone: 'ok' },
  failed: { label: '规划失败', tone: 'bad' },
  discarded: { label: '已放弃', tone: '' },
}
export const rm = (s: string) => requestMeta[s] ?? { label: s, tone: '' }
