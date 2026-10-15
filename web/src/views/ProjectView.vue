<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import TaskGraph from '../components/TaskGraph.vue'
import { roleName, store, toolName } from '../api'
import { boardColumns, statusMeta } from '../status'
import type { Task } from '../types'

const route = useRoute()
const project = computed(() => store.projects.find((p) => p.id === route.params.id))
const view = ref<'graph' | 'board'>('graph')
const selectedId = ref<string>()
const selected = computed(() => project.value?.tasks.find((t) => t.id === selectedId.value))

const columns = computed(() => {
  const ts = project.value?.tasks ?? []
  const cols = boardColumns.map((s) => ({ status: s, tasks: ts.filter((t) => t.status === s) }))
  const blocked = ts.filter((t) => t.status === 'blocked')
  if (blocked.length) cols.unshift({ status: 'blocked', tasks: blocked })
  return cols
})

// 集成队列：待集成的任务按依赖顺序合并到集成分支
const mergeQueue = computed(() => project.value?.tasks.filter((t) => t.status === 'review') ?? [])

const worktree = (t: Task) => `../.ac-worktrees/${project.value?.name}/${t.id}`
const reportsOf = (t: Task) => project.value?.reports.filter((r) => r.taskId === t.id) ?? []
</script>

<template>
  <section v-if="project">
    <div class="page-head">
      <div>
        <div class="faint crumbs"><RouterLink to="/projects">项目</RouterLink> / {{ project.name }}</div>
        <h1>{{ project.name }}</h1>
        <p>{{ project.description }}</p>
        <div class="row small">
          <code>{{ project.path }}</code>
          <span class="badge">⎇ {{ project.baseBranch }}</span>
          <span class="badge primary">⎇ {{ project.integrationBranch }}</span>
          <span v-if="project.remote" class="faint mono">{{ project.remote }}</span>
        </div>
      </div>
      <div class="seg" role="tablist">
        <button role="tab" :aria-selected="view === 'graph'" :class="{ on: view === 'graph' }" @click="view = 'graph'">任务图</button>
        <button role="tab" :aria-selected="view === 'board'" :class="{ on: view === 'board' }" @click="view = 'board'">看板</button>
      </div>
    </div>

    <div class="layout">
      <div class="left">
        <div class="card">
          <TaskGraph v-if="view === 'graph'" :tasks="project.tasks" :selected="selectedId" @select="(id) => (selectedId = id)" />
          <div v-else class="board">
            <div v-for="c in columns" :key="c.status" class="col">
              <div class="col-head"><span class="badge" :class="statusMeta[c.status].tone">{{ statusMeta[c.status].label }}</span><span class="faint">{{ c.tasks.length }}</span></div>
              <button v-for="t in c.tasks" :key="t.id" class="ticket" :class="{ sel: selectedId === t.id }" @click="selectedId = t.id">
                <b>{{ t.id }}</b> {{ t.title }}
                <span class="faint small">{{ toolName(t.assignee) }} · {{ roleName(t.role) }}</span>
              </button>
              <p v-if="!c.tasks.length" class="faint small empty">无</p>
            </div>
          </div>
        </div>

        <div v-if="selected" class="card detail">
          <div class="row between">
            <h2>{{ selected.id }} · {{ selected.title }}</h2>
            <button class="btn" aria-label="关闭" @click="selectedId = undefined">✕</button>
          </div>
          <dl>
            <dt>状态</dt><dd><span class="badge" :class="statusMeta[selected.status].tone">{{ statusMeta[selected.status].label }}</span> {{ selected.progress }}%</dd>
            <dt>负责</dt><dd>{{ toolName(selected.assignee) }}（{{ roleName(selected.role) }}）</dd>
            <dt>依赖</dt><dd>{{ selected.dependsOn.join('、') || '无' }}</dd>
            <dt>分支</dt><dd><code>{{ selected.branch }}</code>（基于 {{ project.integrationBranch }}）</dd>
            <dt>工作区</dt><dd><code>{{ worktree(selected) }}</code></dd>
            <dt>提交</dt><dd>{{ selected.commits }} 个</dd>
          </dl>
          <h2 class="sub">该任务的汇报</h2>
          <ul class="timeline compact">
            <li v-for="r in reportsOf(selected)" :key="r.id">
              <span class="badge" :class="statusMeta[r.status].tone">{{ statusMeta[r.status].label }}</span>
              {{ r.summary }} <code v-if="r.commit" class="faint">{{ r.commit }}</code>
            </li>
            <li v-if="!reportsOf(selected).length" class="faint">暂无汇报</li>
          </ul>
          <div class="row">
            <button class="btn" disabled title="原型阶段未接入">派发给工具</button>
            <button class="btn" disabled title="原型阶段未接入">打开工作区</button>
            <button class="btn" disabled title="原型阶段未接入">查看 diff</button>
          </div>
        </div>
      </div>

      <aside class="right">
        <div class="card">
          <h2>集成队列</h2>
          <p class="faint small">项目管理按依赖顺序把任务分支合并到 <code>{{ project.integrationBranch }}</code>，跑测试通过后再合并到 <code>{{ project.baseBranch }}</code>。</p>
          <ol class="queue">
            <li v-for="t in mergeQueue" :key="t.id"><code>{{ t.branch }}</code> <span class="faint">← {{ toolName(t.assignee) }}</span></li>
            <li v-if="!mergeQueue.length" class="faint">暂无待集成分支</li>
          </ol>
        </div>
        <div class="card">
          <h2>进度汇报</h2>
          <ul class="timeline">
            <li v-for="r in project.reports" :key="r.id">
              <div class="row between small">
                <span><b>{{ toolName(r.from) }}</b> · {{ r.taskId }}</span>
                <span class="faint">{{ r.at }}</span>
              </div>
              <div class="row small"><span class="badge" :class="statusMeta[r.status].tone">{{ statusMeta[r.status].label }}</span><code v-if="r.commit" class="faint">{{ r.commit }}</code></div>
              <p>{{ r.summary }}</p>
            </li>
          </ul>
        </div>
      </aside>
    </div>
  </section>
  <section v-else class="card">
    <p>项目不存在。<RouterLink to="/projects">返回项目列表</RouterLink></p>
  </section>
</template>

<style scoped>
.crumbs { font-size: 12.5px; margin-bottom: 4px; }
.small { font-size: 12.5px; }
.between { justify-content: space-between; }
.seg { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-sm); overflow: hidden; }
.seg button { border: 0; background: var(--surface); color: var(--text-2); padding: 6px 14px; font: inherit; cursor: pointer; }
.seg button.on { background: var(--primary-soft); color: var(--primary); }
.layout { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 16px; align-items: start; }
.left, .right { display: grid; gap: 16px; min-width: 0; }
.left > *, .right > * { min-width: 0; }
.board { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(170px, 1fr); gap: 10px; overflow-x: auto; }
.col { display: grid; gap: 8px; align-content: start; }
.col-head { display: flex; justify-content: space-between; align-items: center; }
.ticket {
  display: grid; gap: 4px; text-align: left; font: inherit; font-size: 13px; color: var(--text);
  background: var(--surface-2); border: 1px solid transparent; border-radius: var(--radius-sm); padding: 8px 10px; cursor: pointer;
}
.ticket:hover, .ticket.sel { border-color: var(--primary); }
.empty { margin: 0; }
.detail h2 { margin: 0; }
.detail .sub { margin: 16px 0 8px; font-size: 13px; }
dl { display: grid; grid-template-columns: 56px 1fr; gap: 6px 10px; margin: 12px 0 0; font-size: 13px; }
dt { color: var(--text-3); }
dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
.timeline { list-style: none; padding: 0; margin: 0; display: grid; gap: 12px; }
.timeline li { border-left: 2px solid var(--border); padding-left: 10px; }
.timeline p { margin: 4px 0 0; font-size: 13px; }
.timeline.compact { gap: 6px; margin-bottom: 12px; font-size: 13px; }
.queue { margin: 0; padding-left: 18px; display: grid; gap: 6px; font-size: 13px; }
@media (max-width: 1080px) { .layout { grid-template-columns: 1fr; } }
</style>
