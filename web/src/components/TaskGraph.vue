<script setup lang="ts">
import { computed } from 'vue'
import { toolName } from '../api'
import type { Task } from '../types'
import { statusMeta } from '../status'

const props = defineProps<{ tasks: Task[]; selected?: string }>()
const emit = defineEmits<{ select: [id: string] }>()

const W = 190, H = 68, GX = 56, GY = 16

// 按依赖深度分层布局：没有依赖的在第 0 列，其余放在最深依赖的下一列。
const layout = computed(() => {
  const byId = new Map(props.tasks.map((t) => [t.id, t]))
  const depth = new Map<string, number>()
  const visit = (id: string, seen = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (seen.has(id)) return 0 // 有环时截断，避免死循环
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
            <path d="M0,0 L10,5 L0,10 z" fill="var(--text-3)" />
          </marker>
        </defs>
        <path v-for="e in layout.edges" :key="e.key" :d="e.d" class="edge" :class="{ done: e.done }" marker-end="url(#arrow)" />
      </svg>
      <button
        v-for="t in tasks" :key="t.id" class="node" :class="[statusMeta[t.status].tone, { sel: selected === t.id }]"
        :style="{ left: layout.pos.get(t.id)!.x + 'px', top: layout.pos.get(t.id)!.y + 'px', width: W + 'px', height: H + 'px' }"
        @click="emit('select', t.id)"
      >
        <span class="top"><b>{{ t.id }}</b> {{ t.title }}</span>
        <span class="bottom">
          <span class="faint">{{ toolName(t.assignee) }}</span>
          <span class="st">{{ statusMeta[t.status].label }}</span>
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
.edge { fill: none; stroke: var(--border); stroke-width: 1.5; }
.edge.done { stroke: var(--success); opacity: 0.6; }
.node {
  position: absolute; display: grid; grid-template-rows: 1fr auto auto; gap: 4px; text-align: left;
  padding: 8px 10px 6px; border-radius: var(--radius-sm); border: 1px solid var(--border);
  border-left: 3px solid var(--tone); background: var(--surface); color: var(--text); font: inherit; font-size: 12.5px; cursor: pointer;
}
.node:hover { border-color: var(--tone); }
.node.sel { box-shadow: 0 0 0 2px var(--primary); }
.top { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.bottom { display: flex; justify-content: space-between; font-size: 12px; }
.st { color: var(--tone); }
.bar { height: 3px; background: var(--surface-2); border-radius: 2px; overflow: hidden; }
.bar span { display: block; height: 100%; background: var(--tone); }
.success { --tone: var(--success); }
.warning { --tone: var(--warning); }
.danger { --tone: var(--danger); }
.info { --tone: var(--info); }
.primary { --tone: var(--primary); }
.neutral { --tone: var(--text-3); }
</style>
