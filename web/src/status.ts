import type { TaskStatus } from './types'

export const statusMeta: Record<TaskStatus, { label: string; tone: string }> = {
  planned: { label: '等待依赖', tone: 'neutral' },
  ready: { label: '可领取', tone: 'primary' },
  running: { label: '开发中', tone: 'info' },
  testing: { label: '测试中', tone: 'warning' },
  review: { label: '待集成', tone: 'primary' },
  merged: { label: '已合并', tone: 'success' },
  blocked: { label: '受阻', tone: 'danger' },
}

export const boardColumns: TaskStatus[] = ['planned', 'ready', 'running', 'testing', 'review', 'merged']
