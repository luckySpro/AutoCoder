<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fmtTokens, store, todayKey, totalTokens } from '../api'
import AddProjectDialog from '../components/AddProjectDialog.vue'
import AgentMark from '../components/AgentMark.vue'
import Icon from '../components/Icon.vue'
import { sm } from '../status'
import type { Project, TaskStatus } from '../types'

const router = useRouter()
const adding = ref(false)

const all = computed(() => store.projects.flatMap((p) => p.tasks))
const count = (s: TaskStatus[]) => all.value.filter((t) => s.includes(t.status)).length
const tokensToday = computed(() => store.usage.filter((u) => u.at.startsWith(todayKey())).reduce((n, u) => n + totalTokens(u.usage), 0))

const stats = computed(() => [
  { label: '进行中的任务', value: count(['running', 'testing', 'integrating']), tone: 'run' },
  { label: '待集成', value: count(['review']), tone: 'accent' },
  { label: '受阻 / 等你处理', value: count(['blocked', 'waiting-user']), tone: 'bad' },
  { label: '今日 Token', value: fmtTokens(tokensToday.value), tone: '' },
])

const order: TaskStatus[] = ['merged', 'integrating', 'review', 'testing', 'running', 'ready', 'waiting-user', 'blocked', 'planned', 'cancelled']
function strip(p: Project) {
  return [...p.tasks].sort((a, b) => order.indexOf(a.status) - order.indexOf(b.status))
}
const stripLabel = (p: Project) => '任务状态：' + p.tasks.map((t) => `${t.id} ${sm(t.status).label}`).join('，')
const done = (p: Project) => p.tasks.filter((t) => t.status === 'merged').length
const crew = (p: Project) => [...new Set(p.tasks.map((t) => t.assignee).filter(Boolean))]
const tokens = (p: Project) => store.usage.filter((u) => u.projectId === p.id).reduce((n, u) => n + totalTokens(u.usage), 0)
const blocked = (p: Project) => p.tasks.filter((t) => t.status === 'blocked' || t.status === 'waiting-user')
// 卡片右下角「等你处理」：受阻任务、等你回答或确认的需求、预算用完
const reqText: Record<string, string> = { questions: '有问题要你回答', proposed: '计划等你确认', failed: '规划没有完成' }
function pending(p: Project) {
  const items = blocked(p).map((t) => ({ id: t.id, text: t.status === 'blocked' ? '受阻' : t.waiting?.includes('确认改动') ? '等你确认改动' : '等你处理', task: true }))
  for (const r of p.requests ?? []) if (reqText[r.status]) items.push({ id: r.id, text: reqText[r.status], task: false })
  if ((p.tokenBudget ?? 0) > 0 && (p.spent ?? 0) >= (p.tokenBudget ?? 0)) items.push({ id: '', text: 'Token 预算已用完', task: false })
  return items
}
const open = (p: Project, it: { id: string; task: boolean }) => router.push(it.task ? `/projects/${p.id}?task=${it.id}` : `/projects/${p.id}`)

function added(id: string) {
  adding.value = false
  router.push(`/projects/${id}`)
}
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <div class="eyebrow">工作台</div>
        <h1>项目</h1>
        <p class="lede">每个项目都是一个 git 仓库：任务各占一条分支，进度随提交汇报，完成后由项目管理合并到集成分支。</p>
      </div>
      <button class="btn primary" @click="adding = true"><Icon name="plus" />添加项目</button>
    </header>

    <div class="stats">
      <div v-for="s in stats" :key="s.label" class="stat" :class="s.tone">
        <span class="v num">{{ s.value }}</span>
        <span class="l">{{ s.label }}</span>
      </div>
    </div>

    <div class="list">
      <div v-if="!store.projects.length" class="panel empty-list">
        <h2>还没有项目</h2>
        <p class="muted">从你的 GitHub 账号或本机目录里选一个仓库，AutoCoder 会建好集成分支，之后每个任务各占一条分支和一个 worktree。</p>
        <button class="btn primary" @click="adding = true"><Icon name="plus" />添加第一个项目</button>
      </div>
      <RouterLink v-for="p in store.projects" :key="p.id" :to="blocked(p).length ? `/projects/${p.id}?task=${blocked(p)[0].id}` : `/projects/${p.id}`" class="proj">
        <div class="id">
          <h2 class="mono">{{ p.name }}</h2>
          <p class="muted">{{ p.description }}</p>
          <div class="row small">
            <span class="branch"><Icon name="branch" />{{ p.baseBranch }}</span>
            <span class="faint mono path">{{ p.path }}</span>
          </div>
        </div>

        <div class="progress">
          <div class="row between small">
            <span><b class="num">{{ done(p) }}</b><span class="faint"> / {{ p.tasks.length }} 已合并</span></span>
          </div>
          <div class="strip" :aria-label="stripLabel(p)">
            <span v-for="t in strip(p)" :key="t.id" :class="['seg', t.status]" :title="`${t.id} ${t.title} · ${sm(t.status).label}`"></span>
          </div>
        </div>

        <div class="side">
          <div class="crew">
            <AgentMark v-for="id in crew(p)" :key="id" :id="id" :size="26" />
          </div>
          <div class="small faint num">{{ fmtTokens(tokens(p)) }} token · {{ p.updatedAt }}</div>
          <div v-if="pending(p).length" class="todo" :aria-label="`${p.name} 等你处理 ${pending(p).length} 件`">
            <span class="todo-head">等你处理 <b class="num">{{ pending(p).length }}</b></span>
            <button v-for="it in pending(p).slice(0, 3)" :key="it.id + it.text" type="button" class="todo-item" @click.prevent.stop="open(p, it)">
              <b v-if="it.id" class="mono">{{ it.id }}</b> {{ it.text }}
            </button>
            <span v-if="pending(p).length > 3" class="small faint">还有 {{ pending(p).length - 3 }} 件</span>
          </div>
        </div>
      </RouterLink>

      <button class="proj add" @click="adding = true">
        <Icon name="github" :size="22" />
        <span>
          <b>从 GitHub 账号添加仓库</b>
          <span class="muted small">像 Claude Code 一样直接选择仓库，也可以选本机目录或新建</span>
        </span>
      </button>
    </div>

    <AddProjectDialog v-if="adding" @close="adding = false" @added="added" />
  </section>
</template>

<style scoped>
.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); border: 1px solid var(--line); border-radius: var(--r-lg); background: var(--surface); overflow: hidden; }
.stat { display: grid; gap: 2px; padding: 16px 18px; border-left: 1px solid var(--line); }
.stat:first-child { border-left: 0; }
.stat .v { font: 600 28px/1.1 var(--font-display); letter-spacing: -0.02em; }
.stat .l { color: var(--ink-3); font-size: 12.5px; }
.stat.run .v { color: var(--run); }
.stat.accent .v { color: var(--accent); }
.stat.bad .v { color: var(--bad); }

.list { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.proj {
  display: grid; grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr) auto; gap: 28px; align-items: center;
  padding: 18px 20px; border-radius: var(--r-lg); border: 1px solid var(--line); background: var(--surface);
  color: var(--ink); text-decoration: none; transition: border-color 0.15s, box-shadow 0.15s;
}
.proj:hover { border-color: var(--line-strong); box-shadow: var(--shadow); text-decoration: none; }
.id { display: grid; grid-template-columns: minmax(0, 1fr); gap: 6px; min-width: 0; }
.id h2 { font-size: 16px; font-weight: 650; }
.path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.progress { display: grid; gap: 8px; }
.strip { display: flex; gap: 3px; height: 10px; }
.seg { flex: 1; border-radius: 3px; background: var(--line); }
.seg.merged { background: var(--ok); }
.seg.review { background: var(--accent); }
.seg.testing { background: var(--warn); }
.seg.running { background: var(--run); }
.seg.ready { background: color-mix(in srgb, var(--accent) 35%, var(--line)); }
.seg.blocked, .seg.waiting-user { background: var(--bad); }
.seg.integrating { background: var(--warn); }
.empty-list { padding: 28px; display: grid; gap: 10px; justify-items: start; }
.side { display: grid; gap: 8px; justify-items: end; }
.crew { display: flex; gap: 4px; }
.todo { display: grid; gap: 2px; justify-items: end; padding: 6px 10px; border: 1px solid var(--bad); border-radius: var(--r); background: color-mix(in srgb, var(--bad) 8%, var(--surface)); max-width: 260px; }
.todo-head { font-size: 11px; letter-spacing: 0.04em; color: var(--bad); }
.todo-item { all: unset; cursor: pointer; font-size: 12px; color: var(--ink); text-align: right; overflow-wrap: anywhere; }
.todo-item:hover { color: var(--bad); text-decoration: underline; }
.todo-item:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: 2px; }
.add {
  grid-template-columns: auto 1fr; gap: 14px; border-style: dashed; background: transparent; cursor: pointer;
  font: inherit; text-align: left; color: var(--ink-2);
}
.add span { display: grid; }
.add b { color: var(--ink); }
.add:hover { background: var(--surface); }
@media (max-width: 960px) {
  .proj { grid-template-columns: minmax(0, 1fr); gap: 14px; }
  .side { display: flex; flex-wrap: wrap; gap: 8px 12px; align-items: center; justify-content: space-between; }
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .stat:nth-child(3) { border-left: 0; }
  .stat:nth-child(n+3) { border-top: 1px solid var(--line); }
}
</style>
