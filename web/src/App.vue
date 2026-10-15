<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { scanTools, store } from './api'

onMounted(() => scanTools())

const installed = computed(() => store.tools.filter((t) => t.installed).length)

const nav = [
  { to: '/projects', label: '项目', icon: '▦' },
  { to: '/roles', label: '角色分配', icon: '◎' },
  { to: '/tools', label: '启动检查', icon: '✓' },
]
</script>

<template>
  <div class="shell">
    <aside class="side">
      <div class="brand">
        <span class="logo" aria-hidden="true">AC</span>
        <div>
          <strong>AutoCoder</strong>
          <div class="faint small">多开发工具协同</div>
        </div>
      </div>
      <nav aria-label="主导航">
        <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="nav-item">
          <span aria-hidden="true" class="icon">{{ n.icon }}</span>{{ n.label }}
        </RouterLink>
      </nav>
      <RouterLink to="/tools" class="tool-summary">
        <span class="dot" :class="{ on: installed > 0 }" aria-hidden="true"></span>
        <span v-if="store.loading">正在检测工具…</span>
        <span v-else>已检测到 {{ installed }} 个工具</span>
        <span v-if="store.demo" class="badge warning">演示</span>
      </RouterLink>
    </aside>
    <main class="main">
      <div v-if="store.demo" class="notice" role="status">
        未连接到本地服务（{{ store.error }}），当前显示演示数据。运行 <code>go run ./cmd/autocoder</code> 后刷新即可看到本机真实检测结果。
      </div>
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.shell { display: grid; grid-template-columns: 220px 1fr; min-height: 100vh; }
.side {
  background: var(--surface); border-right: 1px solid var(--border);
  padding: 16px 12px; display: flex; flex-direction: column; gap: 20px;
  position: sticky; top: 0; height: 100vh;
}
.brand { display: flex; align-items: center; gap: 10px; padding: 0 6px; }
.logo {
  width: 32px; height: 32px; border-radius: 8px; display: grid; place-items: center;
  background: var(--primary); color: #fff; font-weight: 700; font-size: 13px;
}
.small { font-size: 12px; }
nav { display: grid; gap: 2px; }
.nav-item {
  display: flex; align-items: center; gap: 10px; padding: 8px 10px; border-radius: var(--radius-sm);
  color: var(--text-2);
}
.nav-item:hover { background: var(--surface-2); color: var(--text); }
.nav-item.router-link-active { background: var(--primary-soft); color: var(--primary); font-weight: 500; }
.icon { width: 16px; text-align: center; }
.tool-summary {
  margin-top: auto; display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  font-size: 12.5px; color: var(--text-2); padding: 8px 10px; border-radius: var(--radius-sm);
  background: var(--surface-2);
}
.dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-3); }
.dot.on { background: var(--success); }
.main { padding: 24px; min-width: 0; max-width: 1400px; }

@media (max-width: 760px) {
  .shell { grid-template-columns: 1fr; }
  .side { position: static; height: auto; flex-direction: row; flex-wrap: wrap; align-items: center; gap: 8px; padding: 10px 16px; }
  nav { display: flex; gap: 4px; overflow-x: auto; }
  .tool-summary { margin-top: 0; }
  .brand .faint { display: none; }
  .main { padding: 16px; }
}
</style>
