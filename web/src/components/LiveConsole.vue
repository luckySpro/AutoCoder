<script setup lang="ts">
// 一个运行中 CLI 进程的实时输出：状态栏 + "动作 / 原始输出" 两种视图
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { agent } from '../agents'
import { fmtTokens, store, toolName } from '../api'
import { clock, fmtDuration, now, release, retain, runs, stopRun } from '../live'
import { phaseMeta } from '../status'
import AgentMark from './AgentMark.vue'
import Icon from './Icon.vue'

const props = defineProps<{ runId: string; height?: number; focus?: boolean }>()

// 真实服务下按需订阅输出流
retain(props.runId)
watch(() => props.runId, (k, old) => {
  release(old)
  retain(k)
})
onBeforeUnmount(() => release(props.runId))

const run = computed(() => runs.get(props.runId))
const route = useRoute()
// 任务标题；规划中的需求显示需求原文
const title = computed(() => {
  const p = store.projects.find((x) => x.id === run.value?.projectId)
  const id = run.value?.taskId
  return p?.tasks.find((t) => t.id === id)?.title ?? p?.requests?.find((r) => r.id === id)?.text.slice(0, 80)
})
const view = ref<'acts' | 'raw'>('acts')
const follow = ref(true)
const query = ref('')
const confirmStop = ref(false)
const body = ref<HTMLElement>()

const idle = computed(() => (run.value ? now.value - run.value.lastActivity : 0))
const stuckMs = computed(() => (store.settings.idleMinutes ?? 10) * 60_000)
const idleTone = computed(() => {
  const r = run.value
  if (r?.state !== 'running') return r?.phase === 'done' ? 'ok' : r?.phase === 'failed' ? 'bad' : 'dim'
  return idle.value > stuckMs.value ? 'bad' : idle.value > 3 * 60_000 ? 'warn' : 'ok'
})
const idleText = computed(() => {
  const r = run.value
  if (r?.state !== 'running') return phaseMeta[r?.phase ?? 'stopped'] ?? '已停止'
  if (r.phase !== 'running') return phaseMeta[r.phase]
  const s = Math.floor(idle.value / 1000)
  return s < 5 ? '活动中' : `${fmtDuration(idle.value)} 无输出`
})
const elapsed = computed(() => (run.value?.state === 'running' ? now.value : run.value?.lastActivity ?? now.value) - (run.value?.startedAt ?? 0))

const rawLines = computed(() => {
  const q = query.value.trim().toLowerCase()
  const lines = run.value?.raw ?? []
  return q ? lines.filter((l) => l.text.toLowerCase().includes(q)) : lines
})

const icons: Record<string, string> = { say: 'sparkle', read: 'search', edit: 'edit', run: 'terminal', usage: 'usage', warn: 'alert' }
const kindName: Record<string, string> = { say: '回复', read: '读取', edit: '修改', run: '运行', usage: '用量', warn: '提示' }

function onScroll() {
  const el = body.value
  if (!el) return
  follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 24
}
async function toBottom() {
  await nextTick()
  if (body.value && follow.value) body.value.scrollTop = body.value.scrollHeight
}
watch(() => [run.value?.acts.length, run.value?.raw.length, view.value, query.value], toBottom, { immediate: true })

function resume() {
  follow.value = true
  toBottom()
}
function stop() {
  if (!confirmStop.value) {
    confirmStop.value = true
    setTimeout(() => (confirmStop.value = false), 3000)
    return
  }
  confirmStop.value = false
  stopRun(props.runId)
}
</script>

<template>
  <section v-if="run" class="console" :class="{ focus }" :style="{ '--c': agent(run.tool).color }" :aria-label="`${run.taskId} 的实时输出`">
    <header class="bar">
      <AgentMark :id="run.tool" :size="26" />
      <div class="title">
        <b><span class="mono tid">{{ run.taskId }}</span> {{ title }}</b>
        <span class="sub">{{ toolName(run.tool) }} · 第 {{ run.attempt }} 次尝试 · {{ run.state === 'running' ? '已运行' : '用时' }} <span class="num">{{ fmtDuration(elapsed) }}</span></span>
      </div>
      <span class="activity" :class="idleTone" role="status"><i></i>{{ idleText }}</span>
    </header>

    <div class="facts">
      <span><b class="num">{{ run.files.length }}</b> 个文件</span>
      <span class="num"><span class="add">+{{ run.added }}</span> <span class="del">−{{ run.removed }}</span></span>
      <span><b class="num">{{ fmtTokens(run.tokens) }}</b> token</span>
      <span class="grow"></span>
      <div class="seg tiny" role="tablist" aria-label="输出视图">
        <button role="tab" :aria-selected="view === 'acts'" @click="view = 'acts'">动作</button>
        <button role="tab" :aria-selected="view === 'raw'" @click="view = 'raw'">原始输出</button>
      </div>
    </div>

    <div ref="body" class="body" :style="{ height: (height ?? 340) + 'px' }" tabindex="0" @scroll="onScroll">
      <ol v-if="view === 'acts'" class="acts">
        <li v-for="a in run.acts" :key="a.seq" :class="[a.kind, a.tone]">
          <span class="t num">{{ clock(a.at) }}</span>
          <span class="k"><Icon :name="icons[a.kind]" :size="13" />{{ kindName[a.kind] }}</span>
          <span class="x" :class="{ mono: a.kind === 'read' || a.kind === 'edit' || a.kind === 'run' }">{{ a.text }}</span>
          <span v-if="a.meta" class="m">{{ a.meta }}</span>
        </li>
      </ol>
      <pre v-else class="raw"><span v-for="l in rawLines" :key="l.seq" class="ln" :class="[l.tone, { err: l.err }]"><span class="t">{{ clock(l.at) }}</span>{{ l.text }}
</span><span v-if="!rawLines.length" class="ln dim">没有匹配的输出</span></pre>
      <span v-if="run.state === 'running'" class="cursor" aria-hidden="true"></span>
      <p v-else-if="run.result" class="result">{{ run.result }}</p>
      <p v-if="!run.acts.length && !run.raw.length" class="empty">等待输出…</p>
    </div>

    <footer class="foot">
      <input v-if="view === 'raw'" v-model="query" class="find" type="search" placeholder="在输出中搜索" aria-label="在输出中搜索" />
      <button v-if="!follow" class="btn sm" @click="resume"><Icon name="download" />跟随最新</button>
      <span class="grow"></span>
      <RouterLink v-if="route.path !== '/live'" :to="`/live?run=${runId}`" class="btn sm ghost"><Icon name="terminal" />运行监控</RouterLink>
      <button v-if="run.state === 'running'" class="btn sm danger" @click="stop">
        <Icon name="close" />{{ confirmStop ? '再点一次停止' : '停止' }}
      </button>
    </footer>
  </section>
</template>

<style scoped>
.console {
  display: grid; border-radius: var(--r-lg); overflow: hidden; min-width: 0;
  background: var(--term-bg); color: var(--term-ink); border: 1px solid var(--term-line);
  box-shadow: inset 0 2px 0 var(--c);
}
.console.focus { outline: 2px solid var(--c); outline-offset: 2px; }
.bar { display: flex; align-items: center; gap: 10px; padding: 12px 14px 8px; min-width: 0; }
.bar :deep(.mark) { background: color-mix(in srgb, var(--c) 24%, var(--term-bg)); }
.title { display: grid; min-width: 0; flex: 1; line-height: 1.35; }
.title b { font-size: 13.5px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tid { color: var(--c); font-weight: 700; }
.sub { font-size: 12px; color: var(--term-dim); }
.activity { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--term-dim); white-space: nowrap; }
.activity i { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.activity.ok { color: var(--term-ok); }
.activity.ok i { animation: blink 1.4s ease-in-out infinite; }
.activity.warn { color: var(--term-warn); }
.activity.bad { color: var(--term-bad); }
.facts { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; padding: 0 14px 10px; font-size: 12px; color: var(--term-dim); border-bottom: 1px solid var(--term-line); }
.facts b { color: var(--term-ink); font-weight: 600; }
.add { color: var(--term-ok); }
.del { color: var(--term-bad); }
.grow { flex: 1; }
.seg.tiny { background: var(--term-bg-2); border-color: var(--term-line); padding: 2px; }
.seg.tiny button { height: 24px; padding: 0 10px; font-size: 12px; color: var(--term-dim); }
.seg.tiny button[aria-selected=true] { background: var(--term-line); color: var(--term-ink); box-shadow: none; }
.body { overflow: auto; padding: 10px 0; position: relative; outline: none; scrollbar-color: var(--term-line) transparent; }
.body:focus-visible { box-shadow: inset 0 0 0 2px var(--c); }
.acts { list-style: none; margin: 0; padding: 0 14px; display: grid; gap: 2px; }
.acts li { display: grid; grid-template-columns: 58px 62px minmax(0, 1fr) auto; gap: 10px; align-items: baseline; padding: 4px 0; font-size: 12.5px; line-height: 1.5; }
.acts .t { color: var(--term-dim); font: 11px var(--font-mono); }
.acts .k { display: inline-flex; align-items: center; gap: 5px; color: var(--term-dim); font-size: 11.5px; }
.acts .x { min-width: 0; overflow-wrap: anywhere; }
.acts .x.mono { font-size: 12px; }
.acts .m { font: 11.5px var(--font-mono); color: var(--term-dim); white-space: nowrap; }
.acts .say .x { color: var(--term-ink); }
.acts .say .k, .acts .say .k svg { color: var(--c); }
.acts .read .x { color: var(--term-dim); }
.acts .edit .m { color: var(--term-ok); }
.acts .usage .x { color: var(--term-dim); }
.acts .ok .m { color: var(--term-ok); }
.acts .bad .m, .acts .bad .k { color: var(--term-bad); }
.acts .warn .x, .acts .warn .k { color: var(--term-warn); }
.raw { margin: 0; padding: 0 14px; font: 12px/1.6 var(--font-mono); white-space: pre-wrap; overflow-wrap: anywhere; color: var(--term-ink); }
.ln { display: block; }
.ln .t { color: var(--term-dim); opacity: 0.6; margin-right: 12px; user-select: none; }
.ln.dim { color: var(--term-dim); }
.ln.cmd { color: var(--term-cmd); }
.ln.ok { color: var(--term-ok); }
.ln.bad, .ln.err { color: var(--term-bad); }
.ln.warn { color: var(--term-warn); }
.result { margin: 10px 14px 0; padding: 8px 10px; border-left: 2px solid var(--c); font-size: 12.5px; color: var(--term-ink); background: var(--term-bg-2); }
.empty { margin: 0 14px; font: 12px var(--font-mono); color: var(--term-dim); }
.cursor { display: block; width: 7px; height: 14px; margin: 4px 14px 0; background: var(--term-ink); opacity: 0.7; animation: blink 1s steps(1) infinite; }
.foot { display: flex; align-items: center; gap: 8px; padding: 8px 14px; border-top: 1px solid var(--term-line); background: var(--term-bg-2); flex-wrap: wrap; }
.find { height: 28px; min-width: 0; flex: 0 1 220px; background: var(--term-bg); color: var(--term-ink); border: 1px solid var(--term-line); border-radius: var(--r-sm); padding: 0 10px; font: 12px var(--font-mono); }
.foot .btn { background: transparent; border-color: var(--term-line); color: var(--term-ink); }
.foot .btn:hover { background: var(--term-line); }
.foot .btn.danger { color: var(--term-bad); }
.foot .btn.ghost { border-color: transparent; color: var(--term-dim); }
@keyframes blink { 50% { opacity: 0.25; } }
@media (max-width: 560px) {
  .acts li { grid-template-columns: 50px minmax(0, 1fr) auto; }
  .acts .k { display: none; }
}
@media (prefers-reduced-motion: reduce) { .activity.ok i, .result { margin: 10px 14px 0; padding: 8px 10px; border-left: 2px solid var(--c); font-size: 12.5px; color: var(--term-ink); background: var(--term-bg-2); }
.empty { margin: 0 14px; font: 12px var(--font-mono); color: var(--term-dim); }
.cursor { animation: none; } }
</style>
