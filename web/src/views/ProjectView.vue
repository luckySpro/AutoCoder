<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { agent } from '../agents'
import { deliverProject, fmtTokens, logURL, roleName, store, taskAction, toolName, totalTokens, retryBlocked, updateProject, updateTask } from '../api'
import AgentMark from '../components/AgentMark.vue'
import BranchLanes from '../components/BranchLanes.vue'
import Icon from '../components/Icon.vue'
import LiveConsole from '../components/LiveConsole.vue'
import RequestPanel from '../components/RequestPanel.vue'
import TaskForm from '../components/TaskForm.vue'
import { hasRun, runKey, runs } from '../live'
import TaskGraph from '../components/TaskGraph.vue'
import { boardColumns, rm, sm } from '../status'
import type { Report, Task, TaskStatus } from '../types'

const route = useRoute()
const project = computed(() => store.projects.find((p) => p.id === route.params.id))
const view = ref<'lanes' | 'graph' | 'board'>('lanes')
const selectedId = ref<string>()
const selected = computed(() => project.value?.tasks.find((t) => t.id === selectedId.value))
const detailEl = ref<HTMLElement>()
// 选中任务并把详情滚到眼前（托盘和项目列表的「等你处理」会带 ?task=T1 过来）
async function focusTask(id: string) {
  selectedId.value = id
  await nextTick()
  detailEl.value?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
}
// 只在链接里的任务编号变化（或该任务刚加载出来）时跳一次；
// 不能直接 watch 一个新数组，否则每次定时刷新数据都会重新跳到详情
const linked = computed(() => {
  const id = route.query.task
  return typeof id === 'string' && project.value?.tasks.some((t) => t.id === id) ? id : ''
})
watch(linked, (id) => { if (id) focusTask(id) }, { immediate: true })

const columns = computed(() => {
  const ts = project.value?.tasks ?? []
  const cols = boardColumns.map((s) => ({ status: s, tasks: ts.filter((t) => t.status === s) }))
  for (const st of ['cancelled', 'blocked', 'waiting-user'] as TaskStatus[]) {
    const list = ts.filter((t) => t.status === st)
    if (list.length) cols.unshift({ status: st, tasks: list })
  }
  const integ = ts.filter((t) => t.status === 'integrating')
  if (integ.length) cols.find((c) => c.status === 'review')!.tasks.push(...integ)
  return cols
})

// 任务操作
const editing = ref<'new' | Task | null>(null)
const composing = ref(false)
// 汇报的标签：评审结论、需求的规划结果、任务状态
function pill(r: Report) {
  if (r.review === 'pass') return { label: '评审通过', tone: 'ok' }
  if (r.review === 'changes') return { label: '要求修改', tone: 'bad' }
  if (/^R\d+$/.test(r.taskId)) return rm(r.status)
  return sm(r.status)
}
const budget = computed(() => project.value?.tokenBudget ?? 0)
const spent = computed(() => project.value?.spent ?? projectTokens.value)
const overBudget = computed(() => budget.value > 0 && spent.value >= budget.value)
const nearBudget = computed(() => budget.value > 0 && !overBudget.value && spent.value >= budget.value * 0.8)
const busy = ref('')
const notice = ref<{ text: string; bad?: boolean } | null>(null)
async function act(label: string, f: () => Promise<unknown>, ok?: string) {
  busy.value = label
  notice.value = null
  try {
    await f()
    if (ok) notice.value = { text: ok }
  } catch (e) {
    notice.value = { text: (e as Error).message, bad: true }
  } finally {
    busy.value = ''
  }
}
const canEdit = (t: Task) => !['running', 'testing', 'integrating', 'merged'].includes(t.status)
const canRetry = (t: Task) => ['ready', 'blocked', 'cancelled', 'waiting-user', 'planned'].includes(t.status) && !t.waiting?.includes('确认改动')
const canApprove = (t: Task) => t.status === 'waiting-user' && !!t.waiting?.includes('确认改动')
const canCancel = (t: Task) => !['merged', 'cancelled'].includes(t.status)
// 换一个工具接手：先改负责工具，再放回派发队列
const usableTools = computed(() => store.tools.filter((t) => t.installed && !t.disabled && t.headless?.length))
async function retryWith(t: Task, tool: string) {
  if (!project.value || !tool) return
  const id = project.value.id
  await act('retry', async () => {
    if (tool !== t.assignee) await updateTask(id, t.id, { assignee: tool })
    await taskAction(id, t.id, 'retry')
  }, `${t.id} 已交给 ${toolName(tool)} 重新开始`)
}
const retryable = computed(() => waiting.value.filter(canRetry))
async function retryAll() {
  if (!project.value) return
  const id = project.value.id
  await act('retry-all', async () => {
    const r = await retryBlocked(id)
    notice.value = { text: r.retried.length ? `已把 ${r.retried.join('、')} 放回派发队列` : '没有可以重试的任务' }
  })
}
const attempts = (t: Task) => Array.from({ length: t.attempts ?? 0 }, (_, i) => i + 1)
function saved(id: string) {
  editing.value = null
  selectedId.value = id
}
async function deliver() {
  if (!project.value) return
  const id = project.value.id
  await act('deliver', async () => {
    const r = await deliverProject(id)
    notice.value = r.status === 'merged' ? { text: `已交付到 ${project.value?.baseBranch}（${r.commit}），交付说明 ${r.note}` } : { text: r.detail ?? '等你处理', bad: true }
  })
}

// 项目设置
const settingsOpen = ref(false)
const ps = ref({ testCommand: '', setupCommand: '', description: '', budgetK: 0 })
function openSettings() {
  ps.value = {
    testCommand: project.value?.testCommand ?? '', setupCommand: project.value?.setupCommand ?? '', description: project.value?.description ?? '',
    budgetK: Math.round((project.value?.tokenBudget ?? 0) / 1000),
  }
  settingsOpen.value = true
}
async function saveSettings() {
  if (!project.value) return
  const id = project.value.id
  await act('settings', async () => {
    const { budgetK, ...rest } = ps.value
    const patch = { ...rest, tokenBudget: Math.max(0, Math.round(Number(budgetK) || 0)) * 1000 }
    if (store.demo) Object.assign(project.value!, patch)
    else await updateProject(id, patch)
    settingsOpen.value = false
  })
}

const running = computed(() => store.running.filter((r) => r.projectId === project.value?.id))
const mergeQueue = computed(() => project.value?.tasks.filter((t) => t.status === 'review' || t.status === 'integrating') ?? [])
const waiting = computed(() => project.value?.tasks.filter((t) => t.status === 'waiting-user' || t.status === 'blocked') ?? [])
const worktree = (t: Task) => `../.ac-worktrees/${project.value?.name}/${t.id}`
const reportsOf = (t: Task) => project.value?.reports.filter((r) => r.taskId === t.id) ?? []

const usage = computed(() => store.usage.filter((r) => r.projectId === project.value?.id))
const taskTokens = (t: Task) => usage.value.filter((r) => r.taskId === t.id).reduce((n, r) => n + totalTokens(r.usage), 0)
const taskRuns = (t: Task) => usage.value.filter((r) => r.taskId === t.id).length
const projectTokens = computed(() => usage.value.reduce((n, r) => n + totalTokens(r.usage), 0))
const toolTokens = computed(() => {
  const m = new Map<string, number>()
  usage.value.forEach((r) => m.set(r.tool, (m.get(r.tool) ?? 0) + totalTokens(r.usage)))
  return [...m.entries()].sort((a, b) => b[1] - a[1])
})
const lastAct = (projectId: string, taskId: string) => {
  const a = runs.get(runKey(projectId, taskId))?.acts.at(-1)
  return a ? (a.kind === 'say' ? '回复中' : a.text.split('/').pop()) : ''
}
const repoSlug = computed(() => project.value?.remote?.replace(/^.*github\.com[:/]/, '').replace(/\.git$/, ''))
</script>

<template>
  <section v-if="project" class="page">
    <header class="page-head">
      <div>
        <div class="eyebrow"><RouterLink to="/projects">项目</RouterLink> / {{ project.name }}</div>
        <h1 class="mono name">{{ project.name }}</h1>
        <p class="lede">{{ project.description }}</p>
        <div class="row meta">
          <span class="branch"><Icon name="branch" />{{ project.baseBranch }}</span>
          <span class="branch integ"><Icon name="merge" />{{ project.integrationBranch }}</span>
          <span class="faint mono small">{{ project.path }}</span>
          <a v-if="repoSlug" class="small row gh" :href="project.remote?.startsWith('http') ? project.remote : `https://github.com/${repoSlug}`" target="_blank" rel="noopener"><Icon name="github" :size="14" />{{ repoSlug }}</a>
        </div>
      </div>
      <div class="head-actions">
      <button class="btn primary" :aria-expanded="composing" @click="composing = !composing"><Icon name="sparkle" />提需求</button>
      <button class="btn" @click="editing = 'new'"><Icon name="plus" />新建任务</button>
      <div class="seg" role="tablist" aria-label="视图">
        <button role="tab" :aria-selected="view === 'lanes'" @click="view = 'lanes'"><Icon name="branch" :size="15" />分支泳道</button>
        <button role="tab" :aria-selected="view === 'graph'" @click="view = 'graph'">依赖图</button>
        <button role="tab" :aria-selected="view === 'board'" @click="view = 'board'">看板</button>
      </div>
      </div>
    </header>

    <div v-if="notice" class="notice" :class="{ bad: notice.bad }" role="status">
      <span>{{ notice.text }}</span>
      <button class="btn ghost sm" aria-label="关闭提示" @click="notice = null"><Icon name="close" /></button>
    </div>

    <div v-if="waiting.length || overBudget" class="panel waiting">
      <div class="panel-head">
        <h2>等你处理</h2>
        <span class="row head-ops">
          <button v-if="retryable.length > 1" class="btn sm primary" :disabled="!!busy" @click="retryAll"><Icon name="play" />全部重试 {{ retryable.length }}</button>
          <span class="hint num">{{ waiting.length + (overBudget ? 1 : 0) }}</span>
        </span>
      </div>
      <ul>
        <li v-if="overBudget">
          <button class="linkish" @click="openSettings">Token 预算已用完</button>
          <span class="small">已用 {{ fmtTokens(spent) }} / 预算 {{ fmtTokens(budget) }}，这个项目暂停派发。提高预算后自动继续。</span>
        </li>
        <li v-for="t in waiting" :key="t.id">
          <button class="linkish" @click="focusTask(t.id)"><b class="mono">{{ t.id }}</b> {{ t.title }}</button>
          <span class="small msg">{{ t.waiting || t.lastError }}</span>
          <span v-if="t.waiting && t.lastError && !t.waiting.includes(t.lastError)" class="small faint msg">上次的问题：{{ t.lastError }}</span>
          <div class="row quick">
            <button v-if="canApprove(t)" class="btn sm primary" :disabled="!!busy" @click="act('approve', () => taskAction(project!.id, t.id, 'approve'), '已确认，开始验证和合并')"><Icon name="check" />确认改动</button>
            <button v-if="canRetry(t)" class="btn sm primary" :disabled="!!busy" @click="act('retry', () => taskAction(project!.id, t.id, 'retry'), `${t.id} 已放回派发队列`)"><Icon name="play" />重试</button>
            <select v-if="canRetry(t) && usableTools.length > 1" class="sm" :disabled="!!busy" :aria-label="`换一个工具重试 ${t.id}`" @change="retryWith(t, ($event.target as HTMLSelectElement).value); ($event.target as HTMLSelectElement).value = ''">
              <option value="">换工具重试…</option>
              <option v-for="x in usableTools" :key="x.id" :value="x.id" :disabled="x.id === t.assignee">{{ x.name }}</option>
            </select>
            <button v-if="canEdit(t)" class="btn sm" @click="editing = t"><Icon name="edit" />修改说明</button>
            <button class="btn sm ghost" @click="focusTask(t.id)">详情与日志</button>
          </div>
        </li>
      </ul>
    </div>

    <div class="layout">
      <div class="main">
        <RequestPanel :project="project" :composing="composing" @compose="(v) => (composing = v)" @select="(id) => (selectedId = id)" />
        <div v-if="!project.tasks.length && !composing && !project.requests?.some((r) => r.status !== 'discarded')" class="panel empty-tasks">
          <h2>从一句需求开始</h2>
          <p class="muted">写下想做什么，lead 会先读仓库，再拆成一组任务：每个任务交给一个 CLI 工具，在自己的分支 <code>ac/T1-…</code> 和独立的 worktree 里完成，验证、评审通过后合并。</p>
          <div class="row">
            <button class="btn primary" @click="composing = true"><Icon name="sparkle" />说一句需求</button>
            <button class="btn" @click="editing = 'new'"><Icon name="plus" />手动新建任务</button>
          </div>
        </div>
        <div v-else-if="project.tasks.length" class="panel">
          <BranchLanes v-if="view === 'lanes'" :project="project" :selected="selectedId" @select="(id) => (selectedId = id)" />
          <TaskGraph v-else-if="view === 'graph'" :tasks="project.tasks" :selected="selectedId" @select="(id) => (selectedId = id)" />
          <div v-else class="board">
            <div v-for="c in columns" :key="c.status" class="col">
              <div class="col-head"><span class="pill" :class="sm(c.status).tone">{{ sm(c.status).label }}</span><span class="faint num">{{ c.tasks.length }}</span></div>
              <button v-for="t in c.tasks" :key="t.id" class="ticket" :class="{ sel: selectedId === t.id }" :style="{ '--c': agent(t.assignee).color }" @click="selectedId = t.id">
                <span><b class="mono">{{ t.id }}</b> {{ t.title }}</span>
                <AgentMark :id="t.assignee" :size="18" label class="small faint" />
              </button>
              <p v-if="!c.tasks.length" class="faint small">无</p>
            </div>
          </div>
        </div>

        <div v-if="selected" ref="detailEl" class="panel detail" :style="{ '--c': agent(selected.assignee).color }">
          <div class="row between">
            <h2><span class="mono tid">{{ selected.id }}</span> {{ selected.title }}</h2>
            <button class="btn ghost sm" aria-label="关闭详情" @click="selectedId = undefined"><Icon name="close" /></button>
          </div>
          <dl>
            <dt>状态</dt><dd><span class="pill" :class="sm(selected.status).tone">{{ sm(selected.status).label }}</span><span class="num">{{ selected.progress }}%</span></dd>
            <dt>负责</dt><dd><AgentMark :id="selected.assignee" :size="20" label />（{{ roleName(selected.role) }}）</dd>
            <dt>依赖</dt><dd class="mono">{{ selected.dependsOn.join('  ') || '无' }}</dd>
            <dt>分支</dt><dd><span class="branch">{{ selected.branch }}</span><span class="faint small">基于 {{ project.integrationBranch }}</span></dd>
            <dt>工作区</dt><dd class="mono small">{{ worktree(selected) }}</dd>
            <dt>提交</dt><dd class="num">{{ selected.commits }}<span v-if="selected.attempts" class="faint small">第 {{ selected.attempts }} 次尝试</span></dd>
            <dt>Token</dt><dd><b class="num">{{ fmtTokens(taskTokens(selected)) }}</b><span class="faint small">{{ taskRuns(selected) }} 次调用</span><RouterLink to="/usage" class="small">明细</RouterLink></dd>
          </dl>
          <p v-if="selected.waiting" class="alert bad">{{ selected.waiting }}</p>
          <p v-if="selected.lastError && selected.status !== 'merged'" class="alert"><b>上次的问题</b>{{ selected.lastError }}</p>
          <template v-if="selected.description">
            <h3 class="sub">说明</h3>
            <p class="desc">{{ selected.description }}</p>
          </template>
          <div v-if="selected.acceptance?.length || selected.verify?.length || selected.scope?.length" class="specs">
            <div v-if="selected.acceptance?.length">
              <h3 class="sub">验收标准</h3>
              <ol><li v-for="a in selected.acceptance" :key="a">{{ a }}</li></ol>
            </div>
            <div v-if="selected.verify?.length || project.testCommand">
              <h3 class="sub">验证命令</h3>
              <ul class="mono small"><li v-for="v in selected.verify" :key="v">{{ v }}</li><li v-if="project.testCommand" class="faint">{{ project.testCommand }}（项目测试）</li></ul>
            </div>
            <div v-if="selected.scope?.length">
              <h3 class="sub">修改范围</h3>
              <ul class="mono small"><li v-for="v in selected.scope" :key="v">{{ v }}</li></ul>
            </div>
          </div>
          <template v-if="hasRun(project.id, selected.id)">
            <h3 class="sub">{{ selected.status === 'running' || selected.status === 'testing' || selected.status === 'integrating' ? '实时输出' : '最近一次输出' }}</h3>
            <LiveConsole :run-id="runKey(project.id, selected.id)" :height="280" />
          </template>
          <h3 class="sub">汇报</h3>
          <ul class="feed compact">
            <li v-for="r in reportsOf(selected)" :key="r.id">
              <span class="pill" :class="pill(r).tone">{{ pill(r).label }}</span>
              <AgentMark :id="r.from" :size="16" />
              <span class="grow sum">{{ r.summary }}</span>
              <code v-if="r.commit" class="faint">{{ r.commit }}</code>
              <ul v-if="r.issues?.length" class="issues"><li v-for="i in r.issues" :key="i">{{ i }}</li></ul>
            </li>
            <li v-if="!reportsOf(selected).length" class="faint">暂无汇报</li>
          </ul>
          <div class="row actions">
            <button v-if="canApprove(selected)" class="btn sm primary" :disabled="!!busy" @click="act('approve', () => taskAction(project!.id, selected!.id, 'approve'), '已确认，开始验证和合并')"><Icon name="check" />确认改动</button>
            <button v-if="canRetry(selected)" class="btn sm" :class="{ primary: selected.status !== 'ready' }" :disabled="!!busy || selected.status === 'ready'"
              @click="act('retry', () => taskAction(project!.id, selected!.id, 'retry'), '已放回派发队列')"><Icon name="play" />{{ selected.status === 'ready' ? '排队中' : selected.status === 'planned' ? '等依赖' : '重试' }}</button>
            <button v-if="canEdit(selected)" class="btn sm" @click="editing = selected"><Icon name="edit" />修改</button>
            <button v-if="canCancel(selected)" class="btn sm danger" :disabled="!!busy" @click="act('cancel', () => taskAction(project!.id, selected!.id, 'cancel'))"><Icon name="close" />{{ selected.status === 'running' ? '停止' : '取消' }}</button>
            <span class="grow"></span>
            <template v-if="!store.demo">
              <a v-for="n in attempts(selected)" :key="n" class="btn sm ghost" :href="logURL(project.id, selected.id, n)" target="_blank" rel="noopener" :title="`第 ${n} 次尝试的完整日志`"><Icon name="terminal" />日志 {{ n }}</a>
            </template>
          </div>
        </div>
      </div>

      <aside class="aside">
        <div class="panel">
          <div class="panel-head"><h2>正在运行</h2><span class="hint num">{{ running.length }}/{{ store.settings.concurrency }} 槽</span></div>
          <ul class="runs">
            <li v-for="r in running" :key="r.tool + r.taskId">
              <RouterLink :to="`/live?run=${r.projectId}/${r.taskId}`" class="run-link" :title="`查看 ${r.taskId} 的实时输出`">
                <AgentMark :id="r.tool" :size="26" />
                <span class="grow"><b>{{ r.taskId }}</b> <span class="muted">{{ roleName(r.role) }}</span><br /><span class="faint small">{{ toolName(r.tool) }} · {{ r.startedAt }} 开始</span></span>
                <span class="tail mono small faint">{{ lastAct(r.projectId, r.taskId) }}</span>
                <span class="live" aria-label="运行中"></span>
              </RouterLink>
            </li>
            <li v-if="!running.length" class="faint small">没有正在运行的工具</li>
          </ul>
        </div>

        <div class="panel">
          <div class="panel-head"><h2>集成队列</h2><span class="hint">{{ store.settings.autoMerge ? '测试通过后自动合并' : '合并前等你确认' }}</span></div>
          <ol class="queue">
            <li v-for="t in mergeQueue" :key="t.id" :style="{ '--c': agent(t.assignee).color }"><span class="branch">{{ t.branch }}</span></li>
            <li v-if="!mergeQueue.length" class="faint small">暂无待集成分支</li>
          </ol>
          <div v-if="project.deliverable" class="deliver">
            <p class="small">全部任务已合并到 <span class="branch">{{ project.integrationBranch }}</span>。{{ project.testCommand ? (store.settings.autoMerge ? '测试通过会自动交付；也可以现在手动交付。' : '自动合并已关闭。') : '项目没有测试命令，需要你确认后交付。' }}</p>
            <button class="btn primary sm" :disabled="!!busy" @click="deliver"><Icon name="merge" />{{ busy === 'deliver' ? '交付中…' : `交付到 ${project.baseBranch}` }}</button>
          </div>
        </div>

        <div class="panel">
          <div class="panel-head"><h2>项目设置</h2><button v-if="!settingsOpen" class="btn ghost sm" @click="openSettings"><Icon name="edit" />修改</button></div>
          <dl v-if="!settingsOpen" class="psettings">
            <dt>测试</dt><dd class="mono small">{{ project.testCommand || '未设置' }}</dd>
            <dt>准备</dt><dd class="mono small">{{ project.setupCommand || '未设置' }}</dd>
            <dt>预算</dt><dd class="small">{{ budget ? `${fmtTokens(budget)} token` : '不限' }}</dd>
          </dl>
          <form v-else class="psform" @submit.prevent="saveSettings">
            <label class="field">测试命令 <span class="help">合并前与交付前运行</span><input v-model.trim="ps.testCommand" class="mono" placeholder="go test ./..." /></label>
            <label class="field">环境准备 <span class="help">新建任务 worktree 后先运行</span><input v-model.trim="ps.setupCommand" class="mono" placeholder="npm ci --prefix web" /></label>
            <label class="field">Token 预算 <span class="help">千 token；用到 80% 提醒，用完暂停派发，0 表示不限</span><input v-model.number="ps.budgetK" type="number" min="0" step="100" class="mono" /></label>
            <label class="field">项目说明<textarea v-model="ps.description" rows="2"></textarea></label>
            <div class="row"><button class="btn primary sm" :disabled="busy === 'settings'">保存</button><button type="button" class="btn sm" @click="settingsOpen = false">取消</button></div>
          </form>
        </div>

        <div class="panel">
          <div class="panel-head"><h2>Token</h2><RouterLink to="/usage" class="hint">计量明细</RouterLink></div>
          <div class="big num">{{ fmtTokens(projectTokens) }}<span v-if="budget" class="of">/ {{ fmtTokens(budget) }}</span></div>
          <p v-if="nearBudget" class="small warn-text">已用到预算的 {{ Math.round((spent / budget) * 100) }}%</p>
          <div class="stack" aria-hidden="true">
            <span v-for="[id, n] in toolTokens" :key="id" :style="{ flex: n, background: agent(id).color }"></span>
          </div>
          <ul class="legend">
            <li v-for="[id, n] in toolTokens" :key="id"><i :style="{ background: agent(id).color }"></i>{{ toolName(id) }}<span class="num faint">{{ fmtTokens(n) }}</span></li>
          </ul>
        </div>

        <div class="panel">
          <div class="panel-head"><h2>进度汇报</h2></div>
          <ul class="feed">
            <li v-for="r in project.reports" :key="r.id" :style="{ '--c': agent(r.from).color }">
              <div class="row between small"><span class="row"><AgentMark :id="r.from" :size="18" /><b>{{ r.taskId === 'delivery' ? '交付' : r.taskId }}</b></span><span class="faint num">{{ r.at }}</span></div>
              <p>{{ r.summary }}</p>
              <div class="row small"><span class="pill" :class="pill(r).tone">{{ pill(r).label }}</span><code v-if="r.commit" class="faint">{{ r.commit }}</code></div>
            </li>
          </ul>
        </div>
      </aside>
    </div>
  </section>
  <TaskForm v-if="project && editing" :project="project" :task="editing === 'new' ? undefined : editing" @close="editing = null" @saved="saved" />
  <section v-else-if="!project" class="panel">
    <p>没有找到这个项目。<RouterLink to="/projects">返回项目列表</RouterLink></p>
  </section>
</template>

<style scoped>
.name { font-size: 28px; }
.meta { margin-top: 12px; }
.branch.integ { background: var(--accent-soft); color: var(--accent); }
.gh { gap: 4px; color: var(--ink-2); }
.layout { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 16px; align-items: start; }
.main, .aside { display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; min-width: 0; }
.board { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(176px, 1fr); gap: 12px; overflow-x: auto; }
.col { display: grid; gap: 8px; align-content: start; }
.col-head { display: flex; justify-content: space-between; align-items: center; padding-bottom: 4px; }
.ticket {
  display: grid; gap: 8px; text-align: left; font: inherit; font-size: 13px; color: var(--ink); cursor: pointer;
  background: var(--sunken); border: 1px solid var(--line); border-radius: var(--r); padding: 10px 12px; box-shadow: inset 3px 0 0 var(--c);
}
.ticket b { color: var(--c); font-size: 12px; }
.ticket:hover { border-color: var(--line-strong); }
.ticket.sel { border-color: var(--accent); }
.detail h2 { font-size: 17px; }
.tid { color: var(--c); }
.detail .sub { margin: 18px 0 8px; }
dl { display: grid; grid-template-columns: 64px minmax(0, 1fr); gap: 10px 14px; margin: 16px 0 0; font-size: 13px; }
dt { color: var(--ink-3); }
dd { margin: 0; min-width: 0; display: flex; gap: 8px; align-items: center; flex-wrap: wrap; overflow-wrap: anywhere; }
.feed { list-style: none; margin: 0; padding: 0; display: grid; gap: 14px; }
.feed li { display: grid; gap: 6px; padding-left: 12px; border-left: 2px solid var(--c, var(--line)); }
.feed p { font-size: 13px; }
.feed.compact { gap: 8px; margin-bottom: 14px; }
.feed.compact li { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; font-size: 13px; border-left-color: var(--line); }
.runs { list-style: none; margin: 0; padding: 0; display: grid; gap: 10px; }
.runs li { display: flex; gap: 10px; align-items: center; font-size: 13px; line-height: 1.35; }
.grow { flex: 1; min-width: 0; }
.run-link { display: flex; gap: 10px; align-items: center; width: 100%; color: var(--ink); text-decoration: none; padding: 6px; margin: -6px; border-radius: var(--r); }
.run-link:hover { background: var(--sunken); text-decoration: none; }
.tail { max-width: 96px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.live { width: 8px; height: 8px; border-radius: 50%; background: var(--ok); box-shadow: 0 0 0 4px color-mix(in srgb, var(--ok) 20%, transparent); }
.queue { margin: 0; padding-left: 20px; display: grid; gap: 8px; }
.queue li .branch { box-shadow: inset 2px 0 0 var(--c); }
.big { font: 600 30px/1 var(--font-display); letter-spacing: -0.02em; margin-bottom: 12px; }
.stack { display: flex; height: 10px; border-radius: 5px; overflow: hidden; gap: 2px; }
.legend { list-style: none; margin: 12px 0 0; padding: 0; display: grid; gap: 6px; font-size: 13px; }
.legend li { display: flex; align-items: center; gap: 8px; }
.legend i { width: 8px; height: 8px; border-radius: 2px; }
.legend .num { margin-left: auto; }
.head-actions { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.notice { display: flex; justify-content: space-between; align-items: center; gap: 10px; padding: 8px 8px 8px 14px; border: 1px solid var(--ok); background: var(--ok-soft); font-size: 13px; }
.notice.bad { border-color: var(--bad); background: var(--bad-soft); }
.waiting { border-color: var(--bad); }
.waiting ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
.waiting li { display: grid; gap: 4px; font-size: 13px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.waiting .head-ops { gap: 10px; align-items: center; }
.waiting li:last-child { border-bottom: 0; padding-bottom: 0; }
.waiting .msg { white-space: pre-line; overflow-wrap: anywhere; max-height: 6.5em; overflow: auto; }
.waiting .quick { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-top: 2px; }
.waiting select.sm { width: auto; flex: 0 0 auto; height: 28px; font-size: 12px; padding: 0 6px; }
.linkish { font: inherit; background: none; border: 0; padding: 0; color: var(--ink); cursor: pointer; text-align: left; font-weight: 600; }
.linkish:hover { color: var(--accent); }
.empty-tasks { display: grid; gap: 10px; justify-items: start; padding: 28px; }
.empty-tasks p { max-width: 60ch; }
.alert { margin: 14px 0 0; padding: 10px 12px; border-left: 3px solid var(--warn); background: var(--sunken); font-size: 13px; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 200px; overflow: auto; }
.alert b { display: block; font-size: 12px; color: var(--ink-3); margin-bottom: 4px; }
.alert.bad { border-left-color: var(--bad); }
.desc { font-size: 13.5px; white-space: pre-wrap; }
.specs { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 0 20px; }
.specs ol, .specs ul { margin: 0; padding-left: 18px; display: grid; gap: 4px; font-size: 13px; }
.actions { margin-top: 16px; }
.deliver { margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--line); display: grid; gap: 10px; justify-items: start; }
.psettings { grid-template-columns: 40px minmax(0, 1fr); margin: 0; }
.psform { display: grid; gap: 10px; }
.sum { white-space: pre-wrap; }
.issues { flex-basis: 100%; margin: 0; padding-left: 20px; display: grid; gap: 3px; color: var(--ink-2); }
.of { font-size: 14px; color: var(--ink-3); margin-left: 6px; letter-spacing: 0; }
.warn-text { color: var(--warn); margin: -4px 0 10px; }
@media (max-width: 1180px) { .layout { grid-template-columns: 1fr; } }
</style>
