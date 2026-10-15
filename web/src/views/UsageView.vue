<script setup lang="ts">
import { computed, ref } from 'vue'
import { fmtTokens, roleName, store, toolName, totalTokens } from '../api'
import type { TokenUsage, UsageRecord } from '../types'

const project = ref('')
const tool = ref('')

const records = computed(() =>
  store.usage.filter((r) => (!project.value || r.projectId === project.value) && (!tool.value || r.tool === tool.value)),
)

const zero = (): TokenUsage => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 })
function add(a: TokenUsage, b: TokenUsage) {
  a.input += b.input
  a.output += b.output
  a.cacheRead += b.cacheRead
  a.cacheWrite += b.cacheWrite
}

const sum = computed(() => {
  const s = zero()
  records.value.forEach((r) => add(s, r.usage))
  return s
})

function groupBy(key: (r: UsageRecord) => string, label: (k: string) => string) {
  const m = new Map<string, { key: string; label: string; runs: number; usage: TokenUsage; estimated: number }>()
  for (const r of records.value) {
    const k = key(r)
    if (!m.has(k)) m.set(k, { key: k, label: label(k), runs: 0, usage: zero(), estimated: 0 })
    const g = m.get(k)!
    g.runs++
    if (r.estimated) g.estimated++
    add(g.usage, r.usage)
  }
  return [...m.values()].sort((a, b) => totalTokens(b.usage) - totalTokens(a.usage))
}

const projectName = (id: string) => store.projects.find((p) => p.id === id)?.name ?? id
const byTool = computed(() => groupBy((r) => r.tool, toolName))
const byRole = computed(() => groupBy((r) => r.role, (k) => roleName(k as never)))
const byTask = computed(() => groupBy((r) => `${r.projectId}/${r.taskId}`, (k) => {
  const [p, t] = k.split('/')
  const task = store.projects.find((x) => x.id === p)?.tasks.find((x) => x.id === t)
  return `${projectName(p)} · ${t} ${task?.title ?? ''}`
}))

const recent = computed(() => [...records.value].sort((a, b) => b.at.localeCompare(a.at)))
const share = (u: TokenUsage) => (totalTokens(sum.value) ? (totalTokens(u) / totalTokens(sum.value)) * 100 : 0)
const usedTools = computed(() => [...new Set(store.usage.map((r) => r.tool))])

function exportCSV() {
  const head = ['时间', '项目', '任务', '工具', '角色', '输入', '输出', '缓存读取', '缓存写入', '合计', '耗时(秒)', '来源']
  const rows = records.value.map((r) => [
    r.at, projectName(r.projectId), r.taskId, toolName(r.tool), roleName(r.role),
    r.usage.input, r.usage.output, r.usage.cacheRead, r.usage.cacheWrite, totalTokens(r.usage), r.durationSec,
    r.estimated ? '估算' : '工具上报',
  ])
  const csv = '﻿' + [head, ...rows].map((r) => r.map((c) => `"${String(c).replace(/"/g, '""')}"`).join(',')).join('\n')
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }))
  a.download = 'autocoder-token-usage.csv'
  a.click()
  URL.revokeObjectURL(a.href)
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>Token 用量</h1>
        <p>每次派发任务都会记录该次调用消耗的 token，按工具、角色、任务汇总；工具不输出用量时按字符数估算并标注。</p>
      </div>
      <div class="row">
        <select v-model="project" aria-label="按项目筛选">
          <option value="">全部项目</option>
          <option v-for="p in store.projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
        <select v-model="tool" aria-label="按工具筛选">
          <option value="">全部工具</option>
          <option v-for="t in usedTools" :key="t" :value="t">{{ toolName(t) }}</option>
        </select>
        <button class="btn" :disabled="!records.length" @click="exportCSV">导出 CSV</button>
      </div>
    </div>

    <div class="stats">
      <div class="card stat main"><span class="faint">合计</span><b>{{ fmtTokens(totalTokens(sum)) }}</b><span class="faint small">{{ records.length }} 次调用</span></div>
      <div class="card stat"><span class="faint">输入</span><b>{{ fmtTokens(sum.input) }}</b></div>
      <div class="card stat"><span class="faint">输出</span><b>{{ fmtTokens(sum.output) }}</b></div>
      <div class="card stat"><span class="faint">缓存读取</span><b>{{ fmtTokens(sum.cacheRead) }}</b></div>
      <div class="card stat"><span class="faint">缓存写入</span><b>{{ fmtTokens(sum.cacheWrite) }}</b></div>
    </div>

    <p v-if="!records.length" class="card muted">当前筛选条件下没有用量记录。</p>

    <template v-else>
      <div class="two">
        <div class="card">
          <h2>按工具</h2>
          <ul class="bars">
            <li v-for="g in byTool" :key="g.key">
              <div class="row between"><span>{{ g.label }}</span><span class="mono">{{ fmtTokens(totalTokens(g.usage)) }} · {{ share(g.usage).toFixed(0) }}%</span></div>
              <div class="bar"><span :style="{ width: share(g.usage) + '%' }"></span></div>
              <div class="faint small">{{ g.runs }} 次调用 · 输入 {{ fmtTokens(g.usage.input) }} · 输出 {{ fmtTokens(g.usage.output) }}<template v-if="g.estimated"> · {{ g.estimated }} 次为估算</template></div>
            </li>
          </ul>
        </div>
        <div class="card">
          <h2>按角色</h2>
          <ul class="bars">
            <li v-for="g in byRole" :key="g.key">
              <div class="row between"><span>{{ g.label }}</span><span class="mono">{{ fmtTokens(totalTokens(g.usage)) }} · {{ share(g.usage).toFixed(0) }}%</span></div>
              <div class="bar alt"><span :style="{ width: share(g.usage) + '%' }"></span></div>
              <div class="faint small">{{ g.runs }} 次调用</div>
            </li>
          </ul>
        </div>
      </div>

      <div class="card">
        <h2>按任务</h2>
        <div class="table-wrap">
          <table>
            <thead><tr><th>任务</th><th class="r">调用</th><th class="r">输入</th><th class="r">输出</th><th class="r">缓存读取</th><th class="r">合计</th></tr></thead>
            <tbody>
              <tr v-for="g in byTask" :key="g.key">
                <td>{{ g.label }}</td>
                <td class="r">{{ g.runs }}</td>
                <td class="r mono">{{ fmtTokens(g.usage.input) }}</td>
                <td class="r mono">{{ fmtTokens(g.usage.output) }}</td>
                <td class="r mono">{{ fmtTokens(g.usage.cacheRead) }}</td>
                <td class="r mono"><b>{{ fmtTokens(totalTokens(g.usage)) }}</b></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="card">
        <h2>计量明细</h2>
        <div class="table-wrap">
          <table>
            <thead>
              <tr><th>时间</th><th>项目 / 任务</th><th>工具</th><th>角色</th><th class="r">输入</th><th class="r">输出</th><th class="r">缓存读</th><th class="r">缓存写</th><th class="r">合计</th><th class="r">耗时</th><th>来源</th></tr>
            </thead>
            <tbody>
              <tr v-for="r in recent" :key="r.id">
                <td class="mono nowrap">{{ r.at }}</td>
                <td class="nowrap">{{ projectName(r.projectId) }} · {{ r.taskId }}</td>
                <td class="nowrap">{{ toolName(r.tool) }}</td>
                <td class="nowrap">{{ roleName(r.role) }}</td>
                <td class="r mono">{{ r.usage.input.toLocaleString() }}</td>
                <td class="r mono">{{ r.usage.output.toLocaleString() }}</td>
                <td class="r mono">{{ r.usage.cacheRead.toLocaleString() }}</td>
                <td class="r mono">{{ r.usage.cacheWrite.toLocaleString() }}</td>
                <td class="r mono"><b>{{ totalTokens(r.usage).toLocaleString() }}</b></td>
                <td class="r mono">{{ Math.floor(r.durationSec / 60) }}:{{ String(r.durationSec % 60).padStart(2, '0') }}</td>
                <td><span class="badge" :class="r.estimated ? 'warning' : 'success'">{{ r.estimated ? '估算' : '工具上报' }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.stats { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 12px; margin-bottom: 16px; }
.stat { display: grid; gap: 2px; }
.stat b { font-size: 22px; font-variant-numeric: tabular-nums; }
.stat.main b { color: var(--primary); }
.small { font-size: 12px; }
.two { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-bottom: 16px; }
.two > * { min-width: 0; }
.card + .card { margin-top: 16px; }
.two .card + .card, .stats .card { margin-top: 0; }
.between { justify-content: space-between; }
.bars { list-style: none; margin: 0; padding: 0; display: grid; gap: 12px; font-size: 13px; }
.bar { height: 8px; border-radius: 4px; background: var(--surface-2); overflow: hidden; margin: 4px 0 2px; }
.bar span { display: block; height: 100%; background: var(--primary); }
.bar.alt span { background: var(--info); }
.r { text-align: right; }
.nowrap { white-space: nowrap; }
td { font-size: 13px; }
select { min-width: 120px; }
@media (max-width: 900px) {
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .stat.main { grid-column: 1 / -1; }
  .two { grid-template-columns: 1fr; }
}
</style>
