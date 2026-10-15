<script setup lang="ts">
import { computed } from 'vue'
import { agent } from '../agents'
import { toolName } from '../api'
import type { Task } from '../types'
import { statusMeta } from '../status'
import AgentMark from './AgentMark.vue'

const props = defineProps<{ tasks: Task[]; selected?: string }>()
const emit = defineEmits<{ select: [id: string] }>()

const W = 200, H = 78, GX = 60, GY = 14

// 按依赖深度分层：没有依赖的在第 0 列，其余放在最深依赖的下一列。
const layout = computed(() => {
  const byId = new Map(props.tasks.map((t) => [t.id, t]))
  const depth = new Map<string, number>()
  const visit = (id: string, seen = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (seen.has(id)) return 0 // 有环时截断
    seen.add(id)
    const t = byId.get(id)
    const d = t && t.dependsOn.length ? Math.max(...t.dependsOn.map((x) => visit(x, seen))) + 1 : 0
    depth.set(id, d)
    return d
  }
  props.tasks.forEach((t) => visit(t.id))
  const cols: Task[][] = []
  for (const t of props.tasks) (cols[depth.get(t.id)!] ??= []).push(t)
  const pos = new Map<string, { x: number; y: number }>()
  cols.forEach((col, c) => col.forEach((t, r) => pos.set(t.id, { x: c * (W + GX), y: r * (H + GY) })))
  const rows = Math.max(...cols.map((c) => c.length))
  const edges = props.tasks.flatMap((t) =>
    t.dependsOn.filter((d) => pos.has(d)).map((d) => {
      const a = pos.get(d)!, b = pos.get(t.id)!
      const x1 = a.x + W, y1 = a.y + H / 2, x2 = b.x, y2 = b.y + H / 2
      const mx = (x1 + x2) / 2
      return { key: `${d}-${t.id}`, d: `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2 - 6},${y2}`, done: byId.get(d)?.status === 'merged' }
    }),
  )
  return { pos, edges, width: cols.length * (W + GX) - GX, height: rows * (H + GY) - GY }
})
</script>

<template>
  <div class="graph-wrap">
    <div class="graph" :style="{ width: layout.width + 'px', height: layout.height + 'px' }">
      <svg :width="layout.width" :height="layout.height" aria-hidden="true">
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="5" refY="5" markerWidth="6" markerHeight="6" orient="auto">
            <path d="M0,0 L10,5 L0,10 z" class="arrowhead" />
          </marker>
        </defs>
        <path v-for="e in layout.edges" :key="e.key" :d="e.d" class="edge" :class="{ done: e.done }" marker-end="url(#arrow)" />
      </svg>
      <button
        v-for="t in tasks" :key="t.id" class="node" :class="{ sel: selected === t.id, [t.status]: true }"
        :style="{ left: layout.pos.get(t.id)!.x + 'px', top: layout.pos.get(t.id)!.y + 'px', width: W + 'px', height: H + 'px', '--c': agent(t.assignee).color }"
        @click="emit('select', t.id)"
      >
        <span class="top"><b class="mono">{{ t.id }}</b><span class="ttl">{{ t.title }}</span></span>
        <span class="bottom">
          <AgentMark :id="t.assignee" :size="18" />
          <span class="faint small who">{{ toolName(t.assignee) }}</span>
          <span class="pill" :class="statusMeta[t.status].tone">{{ statusMeta[t.status].label }}</span>
        </span>
        <span class="bar"><span :style="{ width: t.progress + '%' }"></span></span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.graph-wrap { overflow: auto; padding: 4px; }
.graph { position: relative; }
svg { position: absolute; inset: 0; overflow: visible; }
.arrowhead { fill: var(--line-strong); }
.edge { fill: none; stroke: var(--line-strong); stroke-width: 1.5; }
.edge.done { stroke: var(--ok); opacity: 0.7; }
.node {
  position: absolute; display: grid; grid-template-rows: auto 1fr auto; gap: 6px; text-align: left;
  padding: 10px 12px 8px; border-radius: var(--r); border: 1px solid var(--line);
  background: var(--surface); color: var(--ink); font: inherit; font-size: 13px; cursor: pointer;
  box-shadow: inset 3px 0 0 var(--c);
}
.node:hover { border-color: var(--line-strong); }
.node.sel { border-color: var(--accent); box-shadow: inset 3px 0 0 var(--c), var(--ring); }
.node.planned { background: var(--sunken); }
.top { display: flex; gap: 6px; min-width: 0; align-items: baseline; }
.top b { color: var(--c); font-size: 12px; }
.ttl { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 550; }
.bottom { display: flex; align-items: center; gap: 6px; min-width: 0; }
.who { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.bar { height: 3px; background: var(--line); border-radius: 2px; overflow: hidden; }
.bar span { display: block; height: 100%; background: var(--c); }
</style>
