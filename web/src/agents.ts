// 每个 CLI 工具的泳道色与字标：在任务图、分支泳道、用量、角色矩阵中保持一致。
const known: Record<string, { color: string; mark: string }> = {
  'claude-code': { color: '#d97757', mark: 'Cl' },
  codex: { color: '#12a37f', mark: 'Cx' },
  'cursor-agent': { color: '#8b7cf6', mark: 'Cu' },
  'gemini-cli': { color: '#4c8df6', mark: 'Ge' },
  'qwen-code': { color: '#e0559b', mark: 'Qw' },
  'copilot-cli': { color: '#8e9bae', mark: 'Co' },
  opencode: { color: '#e9a23b', mark: 'Oc' },
  aider: { color: '#2bb3c0', mark: 'Ai' },
}
const spare = ['#c2703d', '#5a9e3a', '#b85cc9', '#3b8fb0', '#c9a227', '#d0566c']

export function agent(id: string) {
  if (known[id]) return known[id]
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0
  const mark = id.replace(/[^a-z0-9]/gi, '').slice(0, 2)
  return { color: spare[h % spare.length], mark: mark.charAt(0).toUpperCase() + mark.slice(1).toLowerCase() }
}
