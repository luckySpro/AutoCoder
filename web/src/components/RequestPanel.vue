<script setup lang="ts">
// 需求 → 计划：写一句需求，lead 读仓库后拆成任务；问题、计划确认、重新生成都在这里（DESIGN §6.1）
import { computed, ref } from 'vue'
import { agent } from '../agents'
import { createRequest, fmtTokens, requestAction, roleName, savePlan, store, toolName } from '../api'
import { hasRun, runKey } from '../live'
import { rm, sm } from '../status'
import type { Plan, Project, Request } from '../types'
import AgentMark from './AgentMark.vue'
import Icon from './Icon.vue'
import LiveConsole from './LiveConsole.vue'

const props = defineProps<{ project: Project; composing: boolean }>()
const emit = defineEmits<{ compose: [open: boolean]; select: [taskId: string] }>()

const text = ref('')
const busy = ref('')
const error = ref<Record<string, string>>({})
const answers = ref<Record<string, string>>({})
const feedback = ref<Record<string, string>>({})
const asking = ref<Record<string, boolean>>({})
const openPlan = ref<Record<string, boolean>>({})

const reqs = computed(() => props.project.requests ?? [])
const active = computed(() => reqs.value.filter((r) => !['approved', 'discarded'].includes(r.status)))
const done = computed(() => reqs.value.filter((r) => r.status === 'approved').slice(0, 4))
const lead = computed(() => store.assignments.find((a) => a.roles.includes('lead'))?.toolId)

async function run(key: string, f: () => Promise<unknown>) {
  busy.value = key
  error.value = { ...error.value, [key]: '' }
  try {
    await f()
    return true
  } catch (e) {
    error.value = { ...error.value, [key]: (e as Error).message }
    return false
  } finally {
    busy.value = ''
  }
}

async function submit() {
  const t = text.value.trim()
  if (!t) return
  if (await run('new', () => createRequest(props.project.id, t))) {
    text.value = ''
    emit('compose', false)
  }
}

const act = (r: Request, action: 'answer' | 'regenerate' | 'approve' | 'discard', body?: string) =>
  run(r.id, () => requestAction(props.project.id, r.id, action, body))

async function regenerate(r: Request) {
  if (!asking.value[r.id]) {
    asking.value = { ...asking.value, [r.id]: true }
    return
  }
  if (await act(r, 'regenerate', feedback.value[r.id] ?? '')) asking.value = { ...asking.value, [r.id]: false }
}

// 从计划里删掉一个任务：依赖它的任务去掉这条依赖，服务端重新校验
function dropTask(r: Request, id: string) {
  if (!r.plan) return
  const plan: Plan = {
    ...r.plan,
    tasks: r.plan.tasks.filter((t) => t.id !== id).map((t) => ({ ...t, dependsOn: (t.dependsOn ?? []).filter((d) => d !== id) })),
  }
  run(r.id, () => savePlan(props.project.id, r.id, plan))
}

const taskOf = (id: string) => props.project.tasks.find((t) => t.id === id)
const mergedOf = (r: Request) => (r.tasks ?? []).filter((id) => taskOf(id)?.status === 'merged').length
// 计划中每个任务所在的层级：没有依赖的在第 1 层，依赖谁就排在谁后面
function layers(plan: Plan) {
  const depth = new Map<string, number>()
  const by = new Map(plan.tasks.map((t) => [t.id, t]))
  const d = (id: string, seen = new Set<string>()): number => {
    if (depth.has(id)) return depth.get(id)!
    if (seen.has(id)) return 0
    seen.add(id)
    const deps = (by.get(id)?.dependsOn ?? []).filter((x) => by.has(x))
    const v = deps.length ? Math.max(...deps.map((x) => d(x, seen))) + 1 : 0
    depth.set(id, v)
    return v
  }
  plan.tasks.forEach((t) => d(t.id))
  const out: (typeof plan.tasks)[] = []
  for (const t of plan.tasks) (out[depth.get(t.id)!] ??= []).push(t)
  return out.filter(Boolean)
}
</script>

<template>
  <div v-if="composing" class="panel compose">
    <form @submit.prevent="submit">
      <label class="field">
        <span class="row between"><b>说一句需求</b><span class="help">lead{{ lead ? `（${toolName(lead)}）` : '' }}会先读仓库，再拆成可以并行的任务</span></span>
        <textarea v-model="text" rows="4" autofocus placeholder="例如：给后台加订单管理：列表、详情、发货和退款；退款要先退货再退款，附上测试"
          @keydown.meta.enter="submit" @keydown.ctrl.enter="submit"></textarea>
      </label>
      <div class="row between foot">
        <span class="small" :class="error.new ? 'err' : 'faint'">{{ error.new || (store.settings.confirmPlan === false ? '计划生成后直接开始派发' : '计划生成后先给你确认') }}</span>
        <span class="row">
          <button type="button" class="btn sm" @click="emit('compose', false)">取消</button>
          <button class="btn sm primary" :disabled="!text.trim() || busy === 'new'"><Icon name="sparkle" />{{ busy === 'new' ? '提交中…' : '交给 lead 规划' }}</button>
        </span>
      </div>
    </form>
  </div>

  <article v-for="r in active" :key="r.id" class="panel req" :class="r.status" :style="{ '--c': agent(r.tool || lead || '').color }">
    <header class="req-head">
      <span class="mono rid">{{ r.id }}</span>
      <span class="pill" :class="rm(r.status).tone">{{ rm(r.status).label }}</span>
      <span v-if="r.tool" class="row small faint"><AgentMark :id="r.tool" :size="16" />{{ toolName(r.tool) }}</span>
      <span v-if="r.tokens" class="small faint num">{{ fmtTokens(r.tokens) }} token</span>
      <span class="grow"></span>
      <button v-if="r.status !== 'planning'" class="btn ghost sm" :disabled="busy === r.id" title="放弃这条需求" @click="act(r, 'discard')"><Icon name="close" />放弃</button>
    </header>
    <p class="ask">{{ r.text }}</p>

    <!-- 规划中：看 lead 在读什么 -->
    <template v-if="r.status === 'planning'">
      <p v-if="r.errors?.length" class="alert"><b>上一版计划没有通过校验，正在重写（第 {{ r.attempts }} 次）</b>{{ r.errors.join('\n') }}</p>
      <p v-else-if="r.error" class="alert">{{ r.error }}</p>
      <LiveConsole v-if="hasRun(project.id, r.id)" :run-id="runKey(project.id, r.id)" :height="220" />
      <p v-else class="faint small">等待空闲的运行槽和 lead…</p>
    </template>

    <!-- lead 的问题 -->
    <form v-else-if="r.status === 'questions'" class="qa" @submit.prevent="act(r, 'answer', answers[r.id] ?? '')">
      <ol><li v-for="q in r.questions ?? []" :key="q">{{ q }}</li></ol>
      <textarea v-model="answers[r.id]" rows="3" placeholder="逐条回答，或者告诉 lead 按什么假设来"></textarea>
      <div class="row"><button class="btn sm primary" :disabled="!answers[r.id]?.trim() || busy === r.id"><Icon name="check" />回答并重新规划</button></div>
    </form>

    <!-- 计划：等你确认 -->
    <template v-else-if="r.status === 'proposed' && r.plan">
      <div class="plan">
        <div class="layers" role="list" :aria-label="`${r.id} 的任务`">
          <div v-for="(layer, i) in layers(r.plan)" :key="i" class="layer">
            <span class="step mono">{{ i === 0 ? '先' : `第 ${i + 1} 步` }}</span>
            <div class="cards">
            <div v-for="t in layer" :key="t.id" class="pt" role="listitem">
              <div class="row between">
                <span><b class="mono">{{ t.id }}</b> {{ t.title }}</span>
                <button class="btn ghost sm icon" :disabled="busy === r.id || r.plan.tasks.length === 1" :aria-label="`从计划中删掉 ${t.id}`" @click="dropTask(r, t.id)"><Icon name="close" :size="14" /></button>
              </div>
              <div class="row small faint wrap">
                <span>{{ roleName(t.role) }}</span>
                <span v-if="t.dependsOn?.length">依赖 {{ t.dependsOn.join('、') }}</span>
                <span v-if="t.scope?.length" class="mono">{{ t.scope.join(' ') }}</span>
              </div>
              <details v-if="t.description || t.acceptance?.length">
                <summary class="small">说明与验收 {{ t.acceptance?.length ?? 0 }} 条</summary>
                <p v-if="t.description" class="small desc">{{ t.description }}</p>
                <ul class="small"><li v-for="a in t.acceptance ?? []" :key="a">{{ a }}</li></ul>
                <ul v-if="t.verify?.length" class="small mono"><li v-for="v in t.verify" :key="v">{{ v }}</li></ul>
                <p v-else-if="t.manual" class="small faint">人工验收</p>
              </details>
            </div>
            </div>
          </div>
        </div>
        <details class="summary" :open="openPlan[r.id]" @toggle="openPlan[r.id] = ($event.target as HTMLDetailsElement).open">
          <summary class="small">架构说明{{ r.plan.testCommand ? ` · 项目测试 ${r.plan.testCommand}` : '' }}</summary>
          <p class="desc small">{{ r.plan.summary || '（没有说明）' }}</p>
        </details>
        <ul v-if="r.notes?.length" class="notes small">
          <li v-for="n in r.notes" :key="n"><Icon name="alert" :size="13" />{{ n }}</li>
        </ul>
      </div>
      <div v-if="asking[r.id]" class="fb">
        <textarea v-model="feedback[r.id]" rows="2" placeholder="哪里要改？例如：拆得太细，订单和退款合成一个任务"></textarea>
      </div>
      <div class="row actions">
        <button class="btn sm primary" :disabled="busy === r.id" @click="act(r, 'approve')"><Icon name="play" />确认，生成 {{ r.plan.tasks.length }} 个任务</button>
        <button class="btn sm" :disabled="busy === r.id" @click="regenerate(r)"><Icon name="refresh" />{{ asking[r.id] ? '按意见重新生成' : '重新生成' }}</button>
      </div>
    </template>

    <!-- 失败 -->
    <template v-else-if="r.status === 'failed'">
      <p class="alert bad"><b>{{ r.error || '规划没有完成' }}</b>{{ (r.errors ?? []).join('\n') }}</p>
      <div v-if="asking[r.id]" class="fb">
        <textarea v-model="feedback[r.id]" rows="2" placeholder="补充说明，帮 lead 拆得更好（可选）"></textarea>
      </div>
      <div class="row actions"><button class="btn sm primary" :disabled="busy === r.id" @click="regenerate(r)"><Icon name="refresh" />{{ asking[r.id] ? '重新生成' : '重新生成…' }}</button></div>
    </template>

    <p v-if="error[r.id]" class="small err">{{ error[r.id] }}</p>
  </article>

  <div v-if="done.length" class="done-reqs">
    <div v-for="r in done" :key="r.id" class="done-req">
      <span class="mono rid">{{ r.id }}</span>
      <span class="grow ellip">{{ r.text }}</span>
      <span class="chips">
        <button v-for="id in r.tasks ?? []" :key="id" class="chip mono" :class="taskOf(id) ? sm(taskOf(id)!.status).tone : ''" :title="taskOf(id)?.title" @click="emit('select', id)">{{ id }}</button>
      </span>
      <span class="small faint num">{{ mergedOf(r) }}/{{ (r.tasks ?? []).length }} 已合并</span>
    </div>
  </div>
</template>

<style scoped>
.compose form { display: grid; gap: 10px; }
.compose textarea { font-size: 14px; }
.foot { flex-wrap: wrap; gap: 8px; }
.err { color: var(--bad); }
.req { display: grid; grid-template-columns: minmax(0, 1fr); min-width: 0; gap: 12px; box-shadow: inset 4px 0 0 var(--c); }
.req.questions, .req.failed { border-color: var(--bad); }
.req.proposed { border-color: var(--accent); }
.req-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.rid { font-weight: 700; color: var(--c); }
.grow { flex: 1; min-width: 0; }
.ask { font-size: 15px; line-height: 1.55; white-space: pre-wrap; max-width: 72ch; }
.alert { margin: 0; padding: 10px 12px; border-left: 3px solid var(--warn); background: var(--sunken); font-size: 13px; white-space: pre-wrap; overflow-wrap: anywhere; }
.alert b { display: block; font-size: 12px; color: var(--ink-3); margin-bottom: 4px; }
.alert.bad { border-left-color: var(--bad); }
.qa { display: grid; gap: 10px; }
.qa ol { margin: 0; padding-left: 20px; display: grid; gap: 6px; font-weight: 600; }
.plan { display: grid; gap: 12px; }
.layers { display: grid; gap: 10px; }
.layer { display: grid; grid-template-columns: 56px minmax(0, 1fr); gap: 8px; align-items: start; }
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(240px, 100%), 1fr)); gap: 8px; min-width: 0; }
.step { font-size: 11px; color: var(--ink-3); padding-top: 10px; letter-spacing: 0.04em; }
.pt { display: grid; gap: 6px; align-content: start; overflow-wrap: anywhere; padding: 10px 10px 10px 12px; border: 1px solid var(--line); background: var(--sunken); border-radius: var(--r); min-width: 0; }
.pt b { color: var(--accent); font-size: 12px; }
.pt details ul { margin: 6px 0 0; padding-left: 18px; display: grid; gap: 3px; }
.pt summary, .summary summary { cursor: pointer; color: var(--ink-2); }
.wrap { flex-wrap: wrap; gap: 4px 10px; }
.icon { width: 26px; padding: 0; justify-content: center; }
.desc { white-space: pre-wrap; margin-top: 6px; }
.summary { border-top: 1px dashed var(--line); padding-top: 10px; }
.notes { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; color: var(--warn); }
.notes li { display: flex; gap: 6px; align-items: flex-start; }
.fb textarea { width: 100%; }
.actions { gap: 8px; flex-wrap: wrap; }
.done-reqs { display: grid; grid-template-columns: minmax(0, 1fr); min-width: 0; gap: 0; border: 1px solid var(--line); border-radius: var(--r-lg); background: var(--surface); }
.done-req { display: flex; align-items: center; gap: 12px; padding: 10px 14px; font-size: 13px; border-top: 1px solid var(--line); flex-wrap: wrap; }
.done-req:first-child { border-top: 0; }
.ellip { flex: 1 1 12rem; min-width: 0; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chips { display: flex; gap: 4px; flex-wrap: wrap; }
.chip { font: 600 11.5px var(--font-mono); padding: 2px 7px; border: 1px solid var(--line-strong); background: transparent; color: var(--ink-2); cursor: pointer; border-radius: var(--r-sm); }
.chip.ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, var(--line)); }
.chip.run, .chip.accent { color: var(--accent); }
.chip.bad { color: var(--bad); border-color: var(--bad); }
.chip.warn { color: var(--warn); }
@media (max-width: 620px) {
  .layer { grid-template-columns: 1fr; }
  .step { padding-top: 0; }
}
</style>
