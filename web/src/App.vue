<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { agent } from './agents'
import { scanTools, store, toolName } from './api'
import AgentMark from './components/AgentMark.vue'
import Icon from './components/Icon.vue'
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
  { to: '/projects', label: '项目', icon: 'projects' },
  { to: '/live', label: '运行监控', icon: 'terminal' },
  { to: '/usage', label: 'Token 用量', icon: 'usage' },
  { to: '/roles', label: '角色与运行', icon: 'roles' },
  { to: '/tools', label: '工具清单', icon: 'tools' },
]
</script>

<template>
  <div class="shell">
    <aside class="rail">
      <RouterLink to="/projects" class="brand" aria-label="AutoCoder 首页">
        <svg viewBox="0 0 32 32" width="30" height="30" aria-hidden="true">
          <rect width="32" height="32" rx="8" fill="var(--accent)" />
          <path d="M9 8v16M9 12c0 4 4 4 7 4h7M9 20c0-3 3-4 6-4" stroke="var(--accent-ink)" stroke-width="2.2" fill="none" stroke-linecap="round" />
          <circle cx="23" cy="16" r="2.6" fill="var(--accent-ink)" />
        </svg>
        <span>
          <b>AutoCoder</b>
          <small>多工具协同台</small>
        </span>
      </RouterLink>

      <nav aria-label="主导航">
        <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="nav-item">
          <Icon :name="n.icon" />{{ n.label }}
          <span v-if="n.to === '/live' && store.running.length" class="count num">{{ store.running.length }}</span>
        </RouterLink>
      </nav>

      <section class="crew" aria-label="工具编组">
        <div class="rail-label">工具编组</div>
        <p v-if="store.loading && !crew.length" class="rail-empty">正在检测本机工具…</p>
        <p v-else-if="!crew.length" class="rail-empty">还没有可派发的工具。<RouterLink to="/tools">去添加</RouterLink></p>
        <ul>
          <li v-for="t in crew" :key="t.id">
            <AgentMark :id="t.id" :size="24" />
            <span class="who">
              <span class="nm">{{ t.name }}</span>
              <span class="rl">{{ roleOf(t.id).map((r) => roleShort[r]).join(' · ') || '未分配' }}</span>
            </span>
            <span class="state" :class="{ on: busy(t.id) }" :title="busy(t.id) ? `正在处理 ${busy(t.id)!.taskId}` : '空闲'"></span>
          </li>
        </ul>
      </section>

      <section class="slots" aria-label="运行槽">
        <div class="rail-label row between">
          <span>运行槽</span><span class="num">{{ store.running.length }}/{{ store.settings.concurrency }}</span>
        </div>
        <div class="slot-row">
          <template v-for="(s, i) in slots" :key="i">
            <RouterLink v-if="s" :to="`/live?run=${s.projectId}/${s.taskId}`" class="slot used" :style="{ '--c': agent(s.tool).color }"
              :title="`${toolName(s.tool)} · ${s.taskId}，点击查看实时输出`">{{ s.taskId }}</RouterLink>
            <span v-else class="slot" title="空闲"></span>
          </template>
        </div>
      </section>
    </aside>

    <main class="work">
      <div v-if="store.demo" class="notice" role="status">
        <span>当前是演示数据，没有连接到本机的 AutoCoder 服务。在本机运行 <code>autocoder</code> 后打开 <code>127.0.0.1:7788</code> 即可管理真实的工具与仓库。</span>
      </div>
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.shell { display: grid; grid-template-columns: 248px minmax(0, 1fr); min-height: 100vh; }
.rail {
  background: var(--rail); color: var(--rail-ink); padding: 18px 14px;
  display: flex; flex-direction: column; gap: 22px; position: sticky; top: 0; height: 100vh; overflow-y: auto;
}
.brand { display: flex; align-items: center; gap: 10px; color: inherit; padding: 0 6px; text-decoration: none; }
.brand span { display: grid; line-height: 1.2; }
.brand b { font: 650 16px var(--font-display); letter-spacing: -0.01em; }
.brand small { color: var(--rail-ink-2); font-size: 11.5px; }
nav { display: grid; gap: 2px; }
.nav-item {
  display: flex; align-items: center; gap: 10px; height: 36px; padding: 0 10px; border-radius: 9px;
  color: var(--rail-ink-2); font-weight: 500; text-decoration: none;
}
.nav-item:hover { background: var(--rail-2); color: var(--rail-ink); text-decoration: none; }
.nav-item.router-link-active { background: var(--rail-2); color: var(--rail-ink); box-shadow: inset 2px 0 0 var(--accent); }
.rail-label { font: 600 10.5px/1 var(--font-mono); letter-spacing: 0.1em; text-transform: uppercase; color: var(--rail-ink-2); margin: 0 6px 10px; }
.rail-empty { color: var(--rail-ink-2); font-size: 12.5px; margin: 0 6px; }
.crew ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; }
.crew li { display: flex; align-items: center; gap: 10px; padding: 7px 8px; border-radius: 9px; background: var(--rail-2); }
.crew :deep(.mark) { background: color-mix(in srgb, var(--c) 22%, var(--rail)); }
.who { display: grid; min-width: 0; flex: 1; line-height: 1.3; }
.nm { font-size: 13px; font-weight: 550; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rl { font-size: 11.5px; color: var(--rail-ink-2); }
.state { width: 8px; height: 8px; border-radius: 50%; background: var(--rail-line); flex: none; }
.state.on { background: #4fd18b; box-shadow: 0 0 0 3px rgba(79, 209, 139, 0.18); }
.slots { margin-top: auto; }
.slot-row { display: grid; grid-template-columns: repeat(5, 1fr); gap: 6px; padding: 0 6px; }
.slot {
  height: 30px; border-radius: 7px; border: 1px dashed var(--rail-line); display: grid; place-items: center;
  font: 600 11px var(--font-mono); color: var(--rail-ink);
}
.slot.used { border: 1px solid var(--c); background: color-mix(in srgb, var(--c) 22%, var(--rail)); text-decoration: none; }
.slot.used:hover { background: color-mix(in srgb, var(--c) 36%, var(--rail)); }
.count { margin-left: auto; min-width: 20px; height: 18px; padding: 0 6px; border-radius: 9px; display: grid; place-items: center; font-size: 11px; background: #4fd18b; color: #08130d; }
.work { padding: 28px 32px 48px; min-width: 0; display: grid; gap: 20px; align-content: start; max-width: 1440px; }

@media (max-width: 860px) {
  .shell { grid-template-columns: 1fr; }
  .rail { position: static; height: auto; flex-direction: row; flex-wrap: wrap; align-items: center; gap: 10px; padding: 12px 16px; }
  nav { display: flex; overflow-x: auto; flex: 1 1 100%; order: 3; }
  .nav-item { flex: none; }
  .crew, .slots { display: none; }
  .work { padding: 18px 16px 40px; }
}
</style>
