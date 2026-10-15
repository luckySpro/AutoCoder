<script setup lang="ts">
import { computed, nextTick, watch } from 'vue'
import { useRoute } from 'vue-router'
import { store } from '../api'
import LiveConsole from '../components/LiveConsole.vue'
import { runKey, runs } from '../live'

const route = useRoute()
const focusId = computed(() => route.query.run as string | undefined)
const list = computed(() => store.running.map((r) => runKey(r.projectId, r.taskId)).filter((k) => runs.has(k)))
const free = computed(() => Math.max(0, store.settings.concurrency - store.running.length))

watch(focusId, async (id) => {
  await nextTick()
  if (id) document.getElementById(`live-${id}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' })
}, { immediate: true })
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <div class="eyebrow">运行中</div>
        <h1>运行监控</h1>
        <p class="lede">每个运行槽里的 CLI 工具输出都在这里实时显示。"动作"是解析后的读写与命令记录，"原始输出"和在终端里看到的一样。</p>
      </div>
      <span class="pill run num">{{ store.running.length }} / {{ store.settings.concurrency }} 槽在用</span>
    </header>

    <div class="grid">
      <LiveConsole v-for="k in list" :id="`live-${k}`" :key="k" :run-id="k" :height="380" :focus="focusId === k" />
      <div v-for="i in free" :key="'free' + i" class="free">
        <span class="mono">空闲槽</span>
        <span class="small">有任务进入 ready 时会自动派发到这里</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 520px), 1fr)); gap: 16px; align-items: start; }
.free {
  display: grid; place-content: center; gap: 4px; text-align: center; min-height: 160px; border-radius: var(--r-lg);
  border: 1.5px dashed var(--line-strong); color: var(--ink-3);
}
.free .mono { font-weight: 600; color: var(--ink-2); }
</style>
