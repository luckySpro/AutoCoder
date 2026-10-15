<script setup lang="ts">
// 分支泳道：主分支、集成分支，以及每个任务分支从集成分支切出、提交、合并回来的过程。
// 颜色取负责工具的泳道色，横向按依赖深度排列。
import { computed } from 'vue'
import { agent } from '../agents'
import { toolName } from '../api'
import { sm } from '../status'
import type { Project } from '../types'

const props = defineProps<{ project: Project; selected?: string }>()
const emit = defineEmits<{ select: [id: string] }>()

const ROW = 46, TOP = 22, LABEL = 250, COL = 132, RUN = 96

const layout = computed(() => {
  const tasks = props.project.tasks
  const byId = new Map(tasks.map((t) => [t.id, t]))
  const depth = new Map<string, number>()
  const visit = (id: string, seen = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (seen.has(id)) return 0
    seen.add(id)
    const t = byId.get(id)
    const d = t && t.dependsOn.length ? Math.max(...t.dependsOn.map((x) => visit(x, seen))) + 1 : 0
    depth.set(id, d)
    return d
  }
  tasks.forEach((t) => visit(t.id))
  const sorted = [...tasks].sort((a, b) => depth.get(a.id)! - depth.get(b.id)! || a.id.localeCompare(b.id, undefined, { numeric: true }))
  const trunkY = TOP + ROW
  const rows = sorted.map((t, i) => {
    const y = TOP + ROW * (i + 2)
    const x0 = LABEL + 20 + depth.get(t.id)! * COL
    const started = t.status !== 'planned' && t.status !== 'ready'
    const len = started ? Math.max(28, RUN * Math.min(1, 0.25 + t.progress / 100)) : 0
    const x1 = x0 + 18 + len
    const n = Math.min(t.commits, 6)
    const dots = Array.from({ length: n }, (_, k) => x0 + 18 + ((k + 1) * len) / (n + 1))
    const merged = t.status === 'merged'
    return { t, y, x0, x1, dots, merged, started, color: agent(t.assignee).color, mergeX: x1 + 24 }
  })
  const maxDepth = Math.max(0, ...depth.values())
  const width = LABEL + 20 + maxDepth * COL + RUN + 90
  const height = TOP + ROW * (sorted.length + 2)
  return { rows, trunkY, width, height }
})

const clip = (s: string) => (s.length > 14 ? s.slice(0, 13) + '…' : s)

const curve = (x1: number, y1: number, x2: number, y2: number) => {
  const mx = (x1 + x2) / 2
  return `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`
}
</script>

<template>
  <div class="wrap">
    <svg :width="layout.width" :height="layout.height" role="img" :aria-label="`${project.name} 的分支泳道`">
      <!-- 网格 -->
      <line v-for="r in layout.rows" :key="'g' + r.t.id" :x1="LABEL" :x2="layout.width" :y1="r.y" :y2="r.y" class="grid" />
      <!-- 主分支与集成分支 -->
      <text :x="0" :y="TOP + 4" class="lbl strong">{{ project.baseBranch }}</text>
      <line :x1="LABEL" :x2="layout.width - 8" :y1="TOP" :y2="TOP" class="trunk main" />
      <text :x="0" :y="layout.trunkY + 4" class="lbl strong">{{ project.integrationBranch }}</text>
      <path :d="curve(LABEL, TOP, LABEL + 20, layout.trunkY)" class="trunk integ" />
      <line :x1="LABEL + 20" :x2="layout.width - 8" :y1="layout.trunkY" :y2="layout.trunkY" class="trunk integ" />

      <g v-for="r in layout.rows" :key="r.t.id" class="lane" :class="{ sel: selected === r.t.id }" :style="{ '--c': r.color }"
        tabindex="0" role="button" :aria-label="`${r.t.id} ${r.t.title}，${toolName(r.t.assignee)}，${sm(r.t.status).label}`"
        @click="emit('select', r.t.id)" @keydown.enter="emit('select', r.t.id)">
        <rect :x="0" :y="r.y - ROW / 2" :width="layout.width" :height="ROW" class="hit" />
        <text :x="0" :y="r.y - 3" class="title"><tspan class="tid">{{ r.t.id }}</tspan> {{ clip(r.t.title) }}</text>
        <text :x="0" :y="r.y + 13" class="lbl">{{ r.t.branch }}</text>
        <!-- 切出 -->
        <path :d="curve(r.x0, layout.trunkY, r.x0 + 18, r.y)" class="br" :class="{ ghost: !r.started }" />
        <line v-if="r.started" :x1="r.x0 + 18" :x2="r.x1" :y1="r.y" :y2="r.y" class="br" />
        <circle v-for="(x, i) in r.dots" :key="i" :cx="x" :cy="r.y" r="3.6" class="commit" />
        <!-- 合并回集成分支 -->
        <template v-if="r.merged">
          <path :d="curve(r.x1, r.y, r.mergeX, layout.trunkY)" class="br" />
          <circle :cx="r.mergeX" :cy="layout.trunkY" r="5" class="merge" />
        </template>
        <circle v-else :cx="r.started ? r.x1 : r.x0 + 18" :cy="r.y" r="5" class="head" :class="r.t.status" />
      </g>
    </svg>
  </div>
</template>

<style scoped>
.wrap { overflow-x: auto; padding-bottom: 4px; }
svg { display: block; font-family: var(--font-body); }
.grid { stroke: var(--line); stroke-dasharray: 2 4; }
.lbl { font: 500 11px var(--font-mono); fill: var(--ink-3); }
.lbl.strong { fill: var(--ink); font-weight: 600; }
.trunk { fill: none; stroke-width: 3; stroke-linecap: round; }
.trunk.main { stroke: var(--ink); }
.trunk.integ { stroke: var(--accent); }
.br { fill: none; stroke: var(--c); stroke-width: 2.5; stroke-linecap: round; }
.br.ghost { stroke-dasharray: 3 4; opacity: 0.6; }
.commit { fill: var(--surface); stroke: var(--c); stroke-width: 2; }
.merge { fill: var(--c); stroke: var(--surface); stroke-width: 2; }
.head { fill: var(--surface); stroke: var(--c); stroke-width: 2.5; }
.head.running, .head.testing { fill: var(--c); animation: pulse 1.6s ease-in-out infinite; }
.head.blocked { stroke: var(--bad); fill: var(--bad); }
.title { font-size: 13px; font-weight: 550; fill: var(--ink); }
.tid { font-family: var(--font-mono); font-weight: 700; fill: var(--c); }
.hit { fill: transparent; }
.lane { cursor: pointer; outline: none; }
.lane:hover .hit, .lane:focus-visible .hit { fill: var(--sunken); }
.lane.sel .hit { fill: var(--accent-soft); }
@keyframes pulse { 50% { opacity: 0.45; } }
</style>
