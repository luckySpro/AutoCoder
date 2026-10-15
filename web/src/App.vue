<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { agent } from './agents'
import { scanTools, store, toolName } from './api'
import { startLive } from './live'

onMounted(() => {
  scanTools()
  startLive()
})

const crew = computed(() => store.tools.filter((t) => t.installed && !t.disabled && t.headless?.length))
const roleOf = (id: string) => store.assignments.find((a) => a.toolId === id)?.roles ?? []
const roleShort: Record<string, string> = { lead: '主管', developer: '开发', frontend: '前端', tester: '测试', reviewer: '评审' }
const busy = (id: string) => store.running.find((r) => r.tool === id)
const slots = computed(() =>
  Array.from({ length: store.settings.concurrency }, (_, i) => store.running[i] ?? null),
)

const nav = [
  { to: '/projects', label: '项目' },
  { to: '/live', label: '运行监控' },
  { to: '/usage', label: 'Token 用量' },
  { to: '/roles', label: '角色与运行' },
  { to: '/tools', label: '工具清单' },
]
</script>

<template>
  <div class="shell">
    <header class="top">
      <div class="bar">
        <RouterLink to="/projects" class="brand" aria-label="AutoCoder 首页">
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
            <path d="M4 3v18M4 8h7a4 4 0 0 1 4 4v9M4 14h4" stroke="currentColor" stroke-width="2.2" fill="none" stroke-linecap="square" />
            <rect x="17" y="3" width="4" height="4" fill="var(--accent)" />
          </svg>
          <b>AUTOCODER</b>
          <span>调度台</span>
        </RouterLink>
        <nav aria-label="主导航">
          <RouterLink v-for="(n, i) in nav" :key="n.to" :to="n.to" class="nav-item">
            <span class="ix">{{ String(i + 1).padStart(2, '0') }}</span>{{ n.label }}
            <span v-if="n.to === '/live' && store.running.length" class="count num">{{ store.running.length }}</span>
          </RouterLink>
        </nav>
      </div>

      <div class="board">
        <section class="crew" aria-label="工具编组">
          <span class="label">编组</span>
          <p v-if="store.loading && !crew.length" class="empty">正在检测本机工具…</p>
          <p v-else-if="!crew.length" class="empty">还没有可派发的工具，<RouterLink to="/tools">去添加</RouterLink></p>
          <ul>
            <li v-for="t in crew" :key="t.id" :class="{ on: busy(t.id) }" :style="{ '--c': agent(t.id).color }"
              :title="busy(t.id) ? `正在处理 ${busy(t.id)!.taskId}` : '空闲'">
              <i class="lamp"></i>
              <span class="nm">{{ t.name }}</span>
              <span class="rl">{{ roleOf(t.id).map((r) => roleShort[r]).join('·') || '未分配' }}</span>
              <span v-if="busy(t.id)" class="job mono">{{ busy(t.id)!.taskId }}</span>
            </li>
          </ul>
        </section>
        <section class="slots" aria-label="运行槽">
          <span class="label">运行槽 <b class="num">{{ store.running.length }}/{{ store.settings.concurrency }}</b></span>
          <template v-for="(s, i) in slots" :key="i">
            <RouterLink v-if="s" :to="`/live?run=${s.projectId}/${s.taskId}`" class="slot used" :style="{ '--c': agent(s.tool).color }"
              :title="`${toolName(s.tool)} · ${s.taskId}，点击查看实时输出`">{{ s.taskId }}</RouterLink>
            <span v-else class="slot" title="空闲"></span>
          </template>
        </section>
      </div>
    </header>

    <main class="work">
      <div v-if="store.demo" class="notice" role="status">
        <span>当前是演示数据，没有连接到本机的 AutoCoder 服务。在本机运行 <code>autocoder</code> 后打开 <code>127.0.0.1:7788</code> 即可管理真实的工具与仓库。</span>
      </div>
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.shell { min-height: 100vh; }
.top { position: sticky; top: 0; z-index: 10; }
.bar {
  display: flex; align-items: stretch; gap: 28px; background: var(--rail); color: var(--rail-ink);
  padding-inline: max(24px, env(safe-area-inset-left, 0px)) max(24px, env(safe-area-inset-right, 0px));
}
.brand { display: flex; align-items: center; gap: 10px; color: inherit; text-decoration: none; padding: 12px 0; }
.brand b { font: 800 14px/1 var(--font-mono); letter-spacing: 0.18em; }
.brand span { font-size: 12px; color: var(--rail-ink-2); padding-left: 10px; border-left: 1px solid var(--rail-line); }
.brand:hover { text-decoration: none; }
nav { display: flex; align-items: stretch; overflow-x: auto; scrollbar-width: none; }
.nav-item {
  display: flex; align-items: center; gap: 8px; padding: 0 14px; color: var(--rail-ink-2); text-decoration: none;
  font-size: 13.5px; font-weight: 600; white-space: nowrap; border-bottom: 3px solid transparent; border-top: 3px solid transparent;
}
.nav-item .ix { font: 600 10.5px var(--font-mono); color: var(--rail-ink-2); opacity: 0.7; }
.nav-item:hover { color: var(--rail-ink); text-decoration: none; }
.nav-item.router-link-active { color: var(--rail-ink); border-bottom-color: var(--accent); }
.nav-item.router-link-active .ix { color: var(--accent); opacity: 1; }
.count { min-width: 18px; height: 18px; padding: 0 5px; display: grid; place-items: center; font: 700 11px var(--font-mono); background: var(--accent); color: var(--accent-ink); }

.board {
  display: flex; align-items: center; justify-content: space-between; gap: 20px; flex-wrap: wrap;
  background: var(--surface); border-bottom: 1px solid var(--ink); min-height: 44px;
  padding: 6px max(24px, env(safe-area-inset-right, 0px)) 6px max(24px, env(safe-area-inset-left, 0px));
}
.label { font: 600 10.5px/1 var(--font-mono); letter-spacing: 0.12em; text-transform: uppercase; color: var(--ink-3); white-space: nowrap; }
.label b { color: var(--ink); margin-left: 4px; }
.crew { display: flex; align-items: center; gap: 14px; min-width: 0; flex-wrap: wrap; }
.crew ul { list-style: none; margin: 0; padding: 0; display: flex; gap: 6px 18px; flex-wrap: wrap; }
.crew li { display: flex; align-items: center; gap: 7px; font-size: 13px; }
.lamp { width: 9px; height: 9px; background: var(--c); opacity: 0.35; flex: none; }
.crew li.on .lamp { opacity: 1; animation: lamp 1.6s steps(2) infinite; }
.nm { font-weight: 650; }
.rl { color: var(--ink-3); font-size: 12px; }
.job { font-size: 11.5px; font-weight: 700; color: var(--c); }
.empty { font-size: 12.5px; color: var(--ink-3); }
.slots { display: flex; align-items: center; gap: 6px; }
.slots .label { margin-right: 6px; }
.slot {
  width: 40px; height: 26px; display: grid; place-items: center; border: 1px dashed var(--line-strong);
  font: 700 11px var(--font-mono); color: var(--ink); text-decoration: none;
}
.slot.used { border: 1px solid var(--c); background: var(--c); color: #fff; }
.slot.used:hover { background: var(--ink); border-color: var(--ink); color: var(--canvas); text-decoration: none; }
.work {
  padding-block: 28px 56px; padding-inline: max(24px, env(safe-area-inset-left, 0px)) max(24px, env(safe-area-inset-right, 0px));
  min-width: 0; display: grid; gap: 22px; align-content: start; max-width: 1480px; margin: 0 auto;
}
@keyframes lamp { 50% { opacity: 0.4; } }
@media (max-width: 860px) {
  .top { position: static; }
  .crew .label, .slots .label { display: none; }
  .board { gap: 8px; }
  .bar { flex-direction: column; gap: 0; padding-inline: 16px; }
  .brand { padding-bottom: 4px; }
  .nav-item { padding: 0 10px; height: 40px; }
  .nav-item:first-child { padding-left: 0; }
  .board { padding-inline: 16px; }
  .crew .rl { display: none; }
  .work { padding-inline: 16px; padding-block: 18px 40px; }
}
</style>
