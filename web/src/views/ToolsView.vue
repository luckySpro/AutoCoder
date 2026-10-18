<script setup lang="ts">
import { computed, ref } from 'vue'
import { fmtTokens, importTools, removeTool, saveTool, scanTools, specOf, store, strengthName, todayKey, totalTokens, updateManifest } from '../api'
import AgentMark from '../components/AgentMark.vue'
import Icon from '../components/Icon.vue'
import ToolEditor from '../components/ToolEditor.vue'
import type { ToolDetection } from '../types'

const editing = ref<ToolDetection | null>(null)
const creating = ref(false)
const importing = ref(false)
const importText = ref('')
const importError = ref('')
const confirmId = ref('')
const toast = ref('')

const groups = computed(() => [
  { key: 'ready', title: '可派发', hint: '已安装并参与协同', tools: store.tools.filter((t) => t.installed && !t.disabled) },
  { key: 'missing', title: '未安装', hint: '安装后点"重新检测"', tools: store.tools.filter((t) => !t.installed && !t.disabled) },
  { key: 'off', title: '已停用', hint: '保留在清单中，不参与分配', tools: store.tools.filter((t) => t.disabled) },
])
const scannedAt = computed(() => (store.scannedAt ? new Date(store.scannedAt).toLocaleTimeString() : '—'))

function flash(msg: string) {
  toast.value = msg
  setTimeout(() => (toast.value = ''), 2400)
}

async function toggle(t: ToolDetection) {
  await saveTool({ ...specOf(t), disabled: !t.disabled })
  flash(t.disabled ? `已启用 ${t.name}` : `已停用 ${t.name}`)
}

// 卡片上直接切换运行权限
async function setAccess(t: ToolDetection, access: string) {
  await saveTool({ ...specOf(t), access: access === 'standard' ? 'standard' : undefined })
  flash(access === 'standard' ? `${t.name} 改为只改文件，命令要批准` : `${t.name} 改为最高权限`)
}
const accessOf = (t: ToolDetection) => (t.access === 'standard' || !t.fullArgs?.length ? 'standard' : 'full')

// 拉取在线清单：CLI 升级改了参数时不用等 AutoCoder 发新版本
const updating = ref(false)
async function checkArgs() {
  updating.value = true
  try {
    const r = await updateManifest()
    const issues = store.tools.filter((t) => t.installed && t.argIssues?.length).length
    if (r?.error) flash(`在线清单获取失败：${r.error}`)
    else if (r?.changed) flash(issues ? `参数已更新，仍有 ${issues} 个工具的参数不被当前版本认识` : '参数已更新到最新清单')
    else flash(issues ? `清单已是最新，${issues} 个工具的参数不被当前版本认识，可以点编辑修改` : '清单已是最新，参数都能被当前版本识别')
  } catch (e) {
    flash((e as Error).message)
  } finally {
    updating.value = false
  }
}
const remoteAt = computed(() => (store.remote?.checkedAt ? new Date(store.remote.checkedAt).toLocaleString('zh-CN', { hour12: false }) : ''))

async function remove(t: ToolDetection) {
  if (confirmId.value !== t.id) {
    confirmId.value = t.id
    return
  }
  confirmId.value = ''
  await removeTool(t.id)
  flash(t.source === 'custom' ? `已删除 ${t.name}` : `${t.name} 已恢复默认`)
}

async function doImport() {
  importError.value = ''
  try {
    await importTools(importText.value)
    importing.value = false
    importText.value = ''
    flash('已导入清单')
  } catch (e) {
    importError.value = (e as Error).message
  }
}

async function readFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (file) importText.value = await file.text()
}

// 每个工具的用量：今天、累计、调用次数，以及正在做什么
const usageOf = computed(() => {
  const m = new Map<string, { today: number; total: number; calls: number }>()
  for (const u of store.usage) {
    const x = m.get(u.tool) ?? { today: 0, total: 0, calls: 0 }
    const n = totalTokens(u.usage)
    x.total += n
    x.calls++
    if (u.at.startsWith(todayKey())) x.today += n
    m.set(u.tool, x)
  }
  return m
})
const nowDoing = (id: string) => store.running.filter((r) => r.tool === id).map((r) => `${r.projectId} / ${r.taskId}`).join('、')

const sourceLabel = { builtin: '内置', modified: '已修改', custom: '自定义' }
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <div class="eyebrow">启动检查 · {{ scannedAt }}</div>
        <h1>工具清单</h1>
        <p class="lede">AutoCoder 启动时按这份清单检测本机的 CLI 开发工具。在这里增删、停用或调整参数，保存后立即重新检测，不需要改配置文件。</p>
      </div>
      <div class="actions">
        <button class="btn" :disabled="store.loading" @click="scanTools(true)"><Icon name="refresh" />{{ store.loading ? '检测中…' : '重新检测' }}</button>
        <button class="btn" :disabled="updating || store.demo" title="从网上拉取各 CLI 最新的派发参数，并核对本机版本" @click="checkArgs"><Icon name="refresh" />{{ updating ? '检查中…' : '检查参数更新' }}</button>
        <button class="btn" @click="importing = true"><Icon name="upload" />导入</button>
        <a class="btn" :class="{ disabled: store.demo }" :href="store.demo ? undefined : '/api/tools/export'" :aria-disabled="store.demo" download><Icon name="download" />导出</a>
        <button class="btn primary" @click="creating = true"><Icon name="plus" />添加工具</button>
      </div>
    </header>

    <div v-for="g in groups" :key="g.key" class="group" v-show="g.tools.length || g.key === 'ready'">
      <div class="group-head"><h2>{{ g.title }} <span class="faint num">{{ g.tools.length }}</span></h2><span class="faint small">{{ g.hint }}</span></div>
      <details v-if="g.key === 'missing'" class="notfound small">
        <summary>已经装了，这里却显示未安装？</summary>
        <p class="muted">在终端运行 <code>which codex</code>（换成对应命令）看它装在哪。点该工具的“编辑”，把命令改成这个完整路径，保存后会立即重新检测。</p>
        <p class="muted">AutoCoder 在这些目录里找命令：</p>
        <ul class="mono faint"><li v-for="d in store.searchPath" :key="d">{{ d }}</li></ul>
      </details>
      <p v-if="!g.tools.length" class="panel muted">还没有可派发的工具。安装 Claude Code、Codex 等 CLI 后点"重新检测"，或点"添加工具"登记其他工具。</p>
      <div class="rows">
        <article v-for="t in g.tools" :key="t.id" class="tool" :class="{ off: t.disabled }">
          <AgentMark :id="t.id" :size="36" />
          <div class="info">
            <div class="row">
              <h3>{{ t.name }}</h3>
              <span class="faint small">{{ t.vendor }}</span>
              <span class="pill plain outline small">{{ sourceLabel[t.source] }}</span>
            </div>
            <div class="row small">
              <code class="cmd">{{ t.commands[0] }} {{ (t.headless ?? []).join(' ') }}</code>
            </div>
            <div class="row small chips">
              <span v-for="s in t.strengths" :key="s" class="pill plain">{{ strengthName[s] ?? s }}</span>
              <span v-if="t.usageArgs?.length" class="pill ok">精确用量</span>
              <span v-else class="pill warn" title="不输出 token 用量，按字符数估算">估算用量</span>
              <span v-if="!t.headless?.length" class="pill bad">缺少调用参数</span>
              <span v-if="t.installed && t.argIssues?.length" class="pill bad" :title="`当前安装的 v${t.version || '?'} 帮助里找不到这些参数，派发可能失败。点「检查参数更新」，或点编辑修改`">
                当前版本不认识 {{ t.argIssues.join(' ') }}
              </span>
            </div>
          </div>
          <div class="use" :aria-label="`${t.name} 的用量`">
            <span v-if="!usageOf.get(t.id)?.calls && !nowDoing(t.id) && !store.toolState[t.id]" class="faint small">还没用过</span>
            <template v-else>
            <div><span class="faint">今天</span><b class="num">{{ fmtTokens(usageOf.get(t.id)?.today ?? 0) }}</b></div>
            <div><span class="faint">累计</span><b class="num">{{ fmtTokens(usageOf.get(t.id)?.total ?? 0) }}</b><span class="faint num">{{ usageOf.get(t.id)?.calls ?? 0 }} 次</span></div>
            <div v-if="nowDoing(t.id)" class="doing"><i class="live" aria-hidden="true"></i>{{ nowDoing(t.id) }}</div>
            <div v-else-if="store.toolState[t.id]" class="doing warn">{{ store.toolState[t.id] }}</div>
            </template>
          </div>
          <div class="state">
            <template v-if="t.installed">
              <span class="ver mono">v{{ t.version || '?' }}</span>
              <span class="path mono faint" :title="t.path">{{ t.path }}</span>
            </template>
            <span v-else class="faint small">PATH 中未找到 <code>{{ t.commands.join(' / ') }}</code></span>
            <div class="cap" :title="`能力评分 ${t.capability}`"><span :style="{ width: t.capability + '%' }"></span></div>
          </div>
          <div class="ops">
            <select class="access" :value="accessOf(t)" :disabled="store.demo || (!t.fullArgs?.length && accessOf(t) === 'standard')" :aria-label="`${t.name} 的运行权限`"
              :title="t.fullArgs?.length ? '派发任务时工具的权限' : '这个工具没有配置最高权限参数，可在编辑里补上'" @change="setAccess(t, ($event.target as HTMLSelectElement).value)">
              <option value="full">最高权限</option>
              <option value="standard">只改文件</option>
            </select>
            <label class="switch" :title="t.disabled ? '启用' : '停用'">
              <input type="checkbox" :checked="!t.disabled" :aria-label="`${t.name} 参与协同`" @change="toggle(t)" />
              <span></span>
            </label>
            <button class="btn ghost sm" :aria-label="`编辑 ${t.name}`" @click="editing = t"><Icon name="edit" /></button>
            <button v-if="t.source !== 'builtin'" class="btn ghost sm" :class="{ danger: confirmId === t.id }" @click="remove(t)" @blur="confirmId = ''">
              <Icon :name="t.source === 'custom' ? 'trash' : 'undo'" />
              <span v-if="confirmId === t.id">{{ t.source === 'custom' ? '确认删除' : '确认恢复' }}</span>
              <span v-else class="sr-only">{{ t.source === 'custom' ? '删除' : '恢复默认' }}</span>
            </button>
          </div>
        </article>
      </div>
    </div>

    <p class="faint small wrap">清单保存在 <code>{{ store.manifestPath }}</code>，只记录你改动过或新增的条目；内置条目随版本更新。
      各 CLI 的派发参数还会每 6 小时从在线清单自动更新<template v-if="remoteAt">（上次检查 {{ remoteAt }}<template v-if="store.remote?.error">，失败：{{ store.remote.error }}</template>）</template>，你自己改过的参数不会被覆盖。</p>

    <ToolEditor v-if="creating || editing" :tool="editing ?? undefined" @close="creating = false; editing = null" />

    <div v-if="importing" class="scrim" @click.self="importing = false">
      <form class="sheet narrow" role="dialog" aria-modal="true" aria-labelledby="im-title" @submit.prevent="doImport">
        <header class="sheet-head"><h2 id="im-title">导入工具清单</h2><button type="button" class="btn ghost sm" aria-label="关闭" @click="importing = false"><Icon name="close" /></button></header>
        <div class="sheet-body im">
          <p class="muted small">导入会替换你的自定义清单（内置条目不受影响）。可以选择从另一台机器导出的 YAML 文件，或直接粘贴内容。</p>
          <input id="im-file" type="file" accept=".yaml,.yml,text/yaml" @change="readFile" />
          <textarea id="im-text" v-model="importText" class="mono" rows="10" placeholder="tools:&#10;  - id: my-agent&#10;    name: My Agent&#10;    commands: [my-agent]&#10;    headless: [run, &quot;{prompt}&quot;]"></textarea>
        </div>
        <footer class="sheet-foot">
          <span class="small err">{{ importError }}</span>
          <span class="actions"><button type="button" class="btn" @click="importing = false">取消</button><button class="btn primary" :disabled="!importText.trim()">导入</button></span>
        </footer>
      </form>
    </div>

    <div v-if="toast" class="toast" role="status">{{ toast }}</div>
  </section>
</template>

<style scoped>
.group { display: grid; gap: 10px; }
.group-head { display: flex; align-items: baseline; gap: 12px; }
.rows { display: grid; border: 1px solid var(--line); border-radius: var(--r-lg); background: var(--surface); overflow: hidden; }
.rows:empty { display: none; }
.tool {
  display: grid; grid-template-columns: auto minmax(0, 1.4fr) minmax(150px, 0.7fr) minmax(0, 1fr) auto; gap: 18px; align-items: center;
  padding: 16px 18px; border-top: 1px solid var(--line);
}
.tool:first-child { border-top: 0; }
.tool.off { opacity: 0.6; }
.info { display: grid; gap: 6px; min-width: 0; }
.cmd { background: var(--sunken); padding: 2px 8px; border-radius: var(--r-sm); color: var(--ink-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
.chips { gap: 6px; }
.state { display: grid; gap: 4px; min-width: 0; font-size: 12.5px; }
.ver { font-weight: 600; }
.path { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11.5px; }
.cap { height: 4px; border-radius: 2px; background: var(--line); overflow: hidden; margin-top: 4px; max-width: 160px; }
.cap span { display: block; height: 100%; background: var(--ink-2); }
.use { display: grid; gap: 3px; font-size: 12.5px; min-width: 0; }
.use > div { display: flex; gap: 8px; align-items: baseline; }
.use b { font: 600 14px var(--font-mono); }
.doing { color: var(--ok); font-size: 12px; align-items: center !important; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.doing.warn { color: var(--warn); }
.live { width: 7px; height: 7px; border-radius: 50%; background: var(--ok); flex: none; }
.ops { display: flex; align-items: center; gap: 4px; }
.ops .switch { margin-right: 6px; }
a.btn { text-decoration: none; }
.access { height: 28px; font-size: 12px; padding: 0 6px; width: auto; }
a.btn.disabled { opacity: 0.5; pointer-events: none; }
.narrow { width: min(560px, 100%); }
.im { display: grid; gap: 12px; }
.err { color: var(--bad); }
.wrap { overflow-wrap: anywhere; }
.toast {
  position: fixed; bottom: calc(20px + env(safe-area-inset-bottom, 0px)); left: 50%; transform: translateX(-50%); z-index: 30;
  background: var(--ink); color: var(--canvas); padding: 10px 16px; border-radius: var(--r); font-size: 13px; box-shadow: var(--shadow);
}
@media (max-width: 900px) {
  .tool { grid-template-columns: auto minmax(0, 1fr); }
  .use, .state, .ops { grid-column: 2; }
  .use { grid-auto-flow: column; justify-content: start; gap: 14px; }
}
.notfound { margin: 0 0 10px; }
.notfound summary { cursor: pointer; }
.notfound ul { margin: 6px 0 0; padding-left: 18px; max-height: 180px; overflow: auto; word-break: break-all; }
</style>
