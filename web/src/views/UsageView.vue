<script setup lang="ts">
import { computed, ref } from 'vue'
import { agent } from '../agents'
import { fmtTokens, roleName, store, toolName, totalTokens } from '../api'
import AgentMark from '../components/AgentMark.vue'
import Icon from '../components/Icon.vue'
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

const parts = computed(() => [
  { k: 'input', label: '输入', v: sum.value.input },
  { k: 'output', label: '输出', v: sum.value.output },
  { k: 'cacheRead', label: '缓存读取', v: sum.value.cacheRead },
  { k: 'cacheWrite', label: '缓存写入', v: sum.value.cacheWrite },
])
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
  <section class="page">
    <header class="page-head">
      <div>
        <div class="eyebrow">计量</div>
        <h1>Token 用量</h1>
        <p class="lede">每次派发都会记录这次调用消耗的 token，按工具、角色和任务汇总。工具不输出用量时按字符数估算，并标为"估算"。</p>
      </div>
      <div class="actions">
        <select id="f-project" v-model="project" aria-label="按项目筛选">
          <option value="">全部项目</option>
          <option v-for="p in store.projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
        <select id="f-tool" v-model="tool" aria-label="按工具筛选">
          <option value="">全部工具</option>
          <option v-for="t in usedTools" :key="t" :value="t">{{ toolName(t) }}</option>
        </select>
        <button class="btn" :disabled="!records.length" @click="exportCSV"><Icon name="download" />导出 CSV</button>
      </div>
    </header>

    <div class="summary">
      <div class="total">
        <span class="eyebrow">合计</span>
        <span class="v num">{{ fmtTokens(totalTokens(sum)) }}</span>
        <span class="faint small num">{{ records.length }} 次调用</span>
      </div>
      <div class="parts">
        <div v-for="p in parts" :key="p.k" class="part">
          <span class="faint small">{{ p.label }}</span>
          <span class="pv num">{{ fmtTokens(p.v) }}</span>
          <span class="mini"><span :style="{ width: (totalTokens(sum) ? (p.v / totalTokens(sum)) * 100 : 0) + '%' }"></span></span>
        </div>
      </div>
    </div>

    <p v-if="!records.length" class="panel muted">当前筛选条件下没有用量记录。</p>

    <template v-else>
      <div class="two">
        <div class="panel">
          <div class="panel-head"><h2>按工具</h2></div>
          <div class="stack" aria-hidden="true"><span v-for="g in byTool" :key="g.key" :style="{ flex: totalTokens(g.usage), background: agent(g.key).color }"></span></div>
          <ul class="bars">
            <li v-for="g in byTool" :key="g.key">
              <AgentMark :id="g.key" :size="24" />
              <span class="grow">
                <span class="row between"><b>{{ g.label }}</b><span class="mono">{{ fmtTokens(totalTokens(g.usage)) }} <span class="faint">· {{ share(g.usage).toFixed(0) }}%</span></span></span>
                <span class="faint small">{{ g.runs }} 次调用 · 输入 {{ fmtTokens(g.usage.input) }} · 输出 {{ fmtTokens(g.usage.output) }}<template v-if="g.estimated"> · {{ g.estimated }} 次为估算</template></span>
              </span>
            </li>
          </ul>
        </div>
        <div class="panel">
          <div class="panel-head"><h2>按角色</h2></div>
          <ul class="bars">
            <li v-for="g in byRole" :key="g.key" class="role">
              <span class="grow">
                <span class="row between"><b>{{ g.label }}</b><span class="mono">{{ fmtTokens(totalTokens(g.usage)) }} <span class="faint">· {{ share(g.usage).toFixed(0) }}%</span></span></span>
                <span class="bar"><span :style="{ width: share(g.usage) + '%' }"></span></span>
              </span>
            </li>
          </ul>
        </div>
      </div>

      <div class="panel">
        <div class="panel-head"><h2>按任务</h2><span class="hint">按合计从高到低</span></div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>任务</th><th class="r">调用</th><th class="r">输入</th><th class="r">输出</th><th class="r">缓存读取</th><th class="r">合计</th></tr></thead>
            <tbody>
              <tr v-for="g in byTask" :key="g.key">
                <td>{{ g.label }}</td>
                <td class="r num">{{ g.runs }}</td>
                <td class="r mono">{{ fmtTokens(g.usage.input) }}</td>
                <td class="r mono">{{ fmtTokens(g.usage.output) }}</td>
                <td class="r mono">{{ fmtTokens(g.usage.cacheRead) }}</td>
                <td class="r mono"><b>{{ fmtTokens(totalTokens(g.usage)) }}</b></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="panel">
        <div class="panel-head"><h2>计量明细</h2><span class="hint">每行是一次工具调用</span></div>
        <div class="table-wrap">
          <table>
            <thead>
              <tr><th>时间</th><th>项目 / 任务</th><th>工具</th><th>角色</th><th class="r">输入</th><th class="r">输出</th><th class="r">缓存读</th><th class="r">缓存写</th><th class="r">合计</th><th class="r">耗时</th><th>来源</th></tr>
            </thead>
            <tbody>
              <tr v-for="r in recent" :key="r.id">
                <td class="mono nowrap">{{ r.at }}</td>
                <td class="nowrap">{{ projectName(r.projectId) }} · <b class="mono">{{ r.taskId }}</b></td>
                <td class="nowrap"><AgentMark :id="r.tool" :size="18" label /></td>
                <td class="nowrap">{{ roleName(r.role) }}</td>
                <td class="r mono">{{ r.usage.input.toLocaleString() }}</td>
                <td class="r mono">{{ r.usage.output.toLocaleString() }}</td>
                <td class="r mono">{{ r.usage.cacheRead.toLocaleString() }}</td>
                <td class="r mono">{{ r.usage.cacheWrite.toLocaleString() }}</td>
                <td class="r mono"><b>{{ totalTokens(r.usage).toLocaleString() }}</b></td>
                <td class="r mono">{{ Math.floor(r.durationSec / 60) }}:{{ String(r.durationSec % 60).padStart(2, '0') }}</td>
                <td><span class="pill" :class="r.estimated ? 'warn' : 'ok'">{{ r.estimated ? '估算' : '工具上报' }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.summary { display: grid; grid-template-columns: minmax(220px, 0.8fr) 2fr; border: 1px solid var(--line); border-radius: var(--r-lg); background: var(--surface); overflow: hidden; }
.total { display: grid; gap: 6px; padding: 20px 22px; background: var(--rail); color: var(--rail-ink); align-content: center; }
.total .eyebrow { color: var(--rail-ink-2); }
.total .v { font: 600 44px/1 var(--font-display); letter-spacing: -0.03em; }
.total .faint { color: var(--rail-ink-2); }
.parts { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); }
.part { display: grid; gap: 4px; padding: 18px; border-left: 1px solid var(--line); align-content: center; }
.part:first-child { border-left: 0; }
.pv { font: 600 22px/1.1 var(--font-display); }
.mini { height: 4px; border-radius: 2px; background: var(--line); overflow: hidden; }
.mini span { display: block; height: 100%; background: var(--ink-2); }
.two { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.two > * { min-width: 0; }
.stack { display: flex; gap: 2px; height: 12px; border-radius: 6px; overflow: hidden; margin-bottom: 16px; }
.bars { list-style: none; margin: 0; padding: 0; display: grid; gap: 14px; font-size: 13px; }
.bars li { display: flex; gap: 10px; align-items: center; }
.grow { flex: 1; min-width: 0; display: grid; gap: 4px; }
.bar { height: 8px; border-radius: 4px; background: var(--sunken); overflow: hidden; }
.bar span { display: block; height: 100%; background: var(--ink); }
.nowrap { white-space: nowrap; }
select { width: auto; min-width: 128px; height: 34px; padding: 0 10px; }
@media (max-width: 1000px) {
  .summary { grid-template-columns: 1fr; }
  .parts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .part:nth-child(3) { border-left: 0; }
  .part:nth-child(n+3) { border-top: 1px solid var(--line); }
  .two { grid-template-columns: 1fr; }
}
</style>
