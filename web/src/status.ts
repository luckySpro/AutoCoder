import type { TaskStatus } from './types'

// tone 对应 .pill 的语义类
export const statusMeta: Record<TaskStatus, { label: string; tone: string }> = {
  planned: { label: '等待依赖', tone: '' },
  ready: { label: '可派发', tone: 'accent' },
  running: { label: '开发中', tone: 'run' },
  testing: { label: '测试中', tone: 'warn' },
  review: { label: '待集成', tone: 'accent' },
  merged: { label: '已合并', tone: 'ok' },
  blocked: { label: '受阻', tone: 'bad' },
}

export const boardColumns: TaskStatus[] = ['planned', 'ready', 'running', 'testing', 'review', 'merged']
