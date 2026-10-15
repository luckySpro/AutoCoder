<script setup lang="ts">
import { computed } from 'vue'
import { scanTools, store } from '../api'

const installed = computed(() => store.tools.filter((t) => t.installed))
const missing = computed(() => store.tools.filter((t) => !t.installed))
const scannedAt = computed(() => (store.scannedAt ? new Date(store.scannedAt).toLocaleString() : '—'))

const strengthName: Record<string, string> = {
  architecture: '架构', coding: '编码', frontend: '前端', testing: '测试', review: '评审',
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>启动检查</h1>
        <p>每次启动时按工具清单检测本机已安装的开发工具，检测结果用于默认的角色分配。</p>
      </div>
      <button class="btn primary" :disabled="store.loading" @click="scanTools(true)">
        {{ store.loading ? '检测中…' : '重新检测' }}
      </button>
    </div>

    <div class="card meta row">
      <span>清单：<code>{{ store.manifestPath || '内置清单' }}</code></span>
      <span class="faint">·</span>
      <span>检测时间：{{ scannedAt }}</span>
      <span class="faint">·</span>
      <span>已安装 <strong>{{ installed.length }}</strong> / {{ store.tools.length }}</span>
    </div>

    <h2 class="sec">已安装</h2>
    <p v-if="!store.loading && installed.length === 0" class="card muted">
      没有检测到清单中的任何工具。请安装 Claude Code、Codex 等工具后点击"重新检测"，或在清单中添加自定义工具。
    </p>
    <div class="grid">
      <article v-for="t in installed" :key="t.id" class="card tool">
        <header class="row">
          <strong>{{ t.name }}</strong>
          <span class="badge" :class="t.kind === 'cli' ? 'info' : 'primary'">{{ t.kind === 'cli' ? 'CLI' : 'IDE' }}</span>
          <span class="badge success">✓ 已安装</span>
        </header>
        <dl>
          <dt>版本</dt><dd>{{ t.version || '未知' }}</dd>
          <dt>路径</dt><dd class="mono path" :title="t.path">{{ t.path }}</dd>
          <dt>调度</dt>
          <dd>
            <span v-if="t.kind === 'cli' && t.headless?.length">可无人值守派发</span>
            <span v-else class="badge warning">需在工具内手动执行</span>
          </dd>
          <dt>能力</dt>
          <dd>
            <div class="meter" role="meter" :aria-valuenow="t.capability" aria-valuemin="0" aria-valuemax="100" :aria-label="`${t.name} 能力评分`">
              <span :style="{ width: t.capability + '%' }"></span>
            </div>
            <span class="faint">{{ t.capability }}</span>
          </dd>
          <dt>擅长</dt>
          <dd class="row"><span v-for="s in t.strengths" :key="s" class="badge">{{ strengthName[s] ?? s }}</span></dd>
        </dl>
        <p v-if="t.error" class="err">{{ t.error }}</p>
      </article>
    </div>

    <h2 class="sec">未检测到</h2>
    <div class="card table-wrap">
      <table>
        <thead><tr><th>工具</th><th>厂商</th><th>类型</th><th>查找命令</th></tr></thead>
        <tbody>
          <tr v-for="t in missing" :key="t.id">
            <td>{{ t.name }}</td>
            <td class="muted">{{ t.vendor }}</td>
            <td>{{ t.kind.toUpperCase() }}</td>
            <td><code>{{ t.commands.join(' / ') }}</code></td>
          </tr>
        </tbody>
      </table>
    </div>

    <details class="card howto">
      <summary>如何添加常用工具到清单</summary>
      <p class="muted">在 <code>~/.autocoder/tools.yaml</code> 中按 id 覆盖或追加条目，下次启动自动生效：</p>
      <pre><code>tools:
  - id: my-agent
    name: My Agent
    kind: cli
    commands: [my-agent]
    version_args: ["--version"]
    headless: ["run", "{prompt}"]
    capability: 80
    strengths: [coding, testing]
  - id: aider
    disabled: true   # 不想参与协同的工具</code></pre>
    </details>
  </section>
</template>

<style scoped>
.meta { gap: 10px; font-size: 13px; margin-bottom: 8px; }
.sec { margin: 24px 0 10px; }
.tool header { margin-bottom: 10px; }
dl { display: grid; grid-template-columns: 44px 1fr; gap: 6px 10px; margin: 0; font-size: 13px; }
dt { color: var(--text-3); }
dd { margin: 0; min-width: 0; display: flex; align-items: center; gap: 8px; }
.path { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: block; }
.meter { flex: 1; height: 6px; border-radius: 3px; background: var(--surface-2); overflow: hidden; }
.meter span { display: block; height: 100%; background: var(--primary); }
.err { color: var(--danger); font-size: 12.5px; margin: 8px 0 0; }
.howto { margin-top: 24px; }
.howto summary { cursor: pointer; font-weight: 500; }
pre { background: var(--surface-2); padding: 12px; border-radius: var(--radius-sm); overflow-x: auto; }
</style>
