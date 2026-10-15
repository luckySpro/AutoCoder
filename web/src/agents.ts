// 每个 CLI 工具的泳道色与字标：在任务图、分支泳道、用量、角色矩阵中保持一致。
const known: Record<string, { color: string; mark: string }> = {
  'claude-code': { color: '#cf6a43', mark: 'Cl' },
  codex: { color: '#0f8a6a', mark: 'Cx' },
  'cursor-agent': { color: '#6d5dd3', mark: 'Cu' },
  'gemini-cli': { color: '#2f6fd6', mark: 'Ge' },
  'qwen-code': { color: '#c23f83', mark: 'Qw' },
  'copilot-cli': { color: '#66788f', mark: 'Co' },
  opencode: { color: '#c98612', mark: 'Oc' },
  aider: { color: '#13909c', mark: 'Ai' },
}
const spare = ['#c2703d', '#5a9e3a', '#b85cc9', '#3b8fb0', '#c9a227', '#d0566c']

export function agent(id: string) {
  if (known[id]) return known[id]
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0
  const mark = id.replace(/[^a-z0-9]/gi, '').slice(0, 2)
  return { color: spare[h % spare.length], mark: mark.charAt(0).toUpperCase() + mark.slice(1).toLowerCase() }
}
