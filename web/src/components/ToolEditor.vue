<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { STRENGTHS, saveTool, store, strengthName } from '../api'
import type { ToolDetection, ToolSpec } from '../types'
import AgentMark from './AgentMark.vue'
import Icon from './Icon.vue'

const props = defineProps<{ tool?: ToolDetection }>()
const emit = defineEmits<{ close: [] }>()

const isNew = !props.tool
const join = (a?: string[]) => (a ?? []).map((x) => (/\s/.test(x) ? `"${x}"` : x)).join(' ')
// 按空格拆分参数，支持用双引号包住含空格的参数
const split = (s: string) => [...s.matchAll(/"([^"]*)"|(\S+)/g)].map((m) => m[1] ?? m[2])

const f = reactive({
  id: props.tool?.id ?? '',
  name: props.tool?.name ?? '',
  vendor: props.tool?.vendor ?? '',
  commands: (props.tool?.commands ?? []).join(' '),
  versionArgs: join(props.tool?.versionArgs ?? ['--version']),
  headless: join(props.tool?.headless ?? ['-p', '{prompt}']),
  usageArgs: join(props.tool?.usageArgs),
  paths: [...new Set(Object.values(props.tool?.paths ?? {}).flat())].join('\n'),
  capability: props.tool?.capability ?? 70,
  strengths: [...(props.tool?.strengths ?? ['coding'])],
  disabled: props.tool?.disabled ?? false,
})
const saving = ref(false)
const serverError = ref('')
const touched = ref(false)

const errors = computed(() => {
  const e: Record<string, string> = {}
  if (!/^[a-z0-9][a-z0-9-]{0,39}$/.test(f.id)) e.id = '小写字母、数字和连字符，最长 40 个字符'
  else if (isNew && store.tools.some((t) => t.id === f.id)) e.id = '这个标识已经存在'
  if (!f.name.trim()) e.name = '请填写名称'
  const cmds = split(f.commands)
  if (!cmds.length) e.commands = '至少填写一个可执行文件名'
  else if (cmds.some((c) => /[/\\]/.test(c))) e.commands = '只填文件名；绝对路径写在下方的安装路径里'
  if (f.headless.trim() && !split(f.headless).includes('{prompt}')) e.headless = '必须包含 {prompt} 占位符'
  return e
})

const firstCmd = computed(() => split(f.commands)[0] || '<命令>')
const preview = computed(() => {
  const args = split(f.headless).map((a) => (a === '{prompt}' ? '"<任务说明>"' : a))
  return [firstCmd.value, ...args, ...split(f.usageArgs)].join(' ')
})

function toSpec(): ToolSpec {
  const lines = f.paths.split('\n').map((s) => s.trim()).filter(Boolean)
  // 每个系统只检查自己的路径；界面不区分系统，三处写同一份，不存在的路径会被忽略
  const paths = lines.length ? { darwin: lines, linux: lines, windows: lines } : undefined
  return {
    id: f.id, name: f.name.trim(), vendor: f.vendor.trim(), commands: split(f.commands), paths,
    versionArgs: split(f.versionArgs), headless: split(f.headless), usageArgs: split(f.usageArgs).length ? split(f.usageArgs) : undefined,
    capability: Number(f.capability), strengths: f.strengths, disabled: f.disabled,
  }
}

async function submit() {
  touched.value = true
  if (Object.keys(errors.value).length) return
  saving.value = true
  serverError.value = ''
  try {
    await saveTool(toSpec())
    emit('close')
  } catch (e) {
    serverError.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
const err = (k: string) => (touched.value ? errors.value[k] : '')
</script>

<template>
  <div class="scrim drawer-scrim" @click.self="emit('close')">
    <form class="sheet drawer" role="dialog" aria-modal="true" aria-labelledby="ed-title" @submit.prevent="submit" novalidate>
      <header class="sheet-head">
        <div class="row">
          <AgentMark v-if="f.id" :id="f.id" :size="30" />
          <div>
            <h2 id="ed-title">{{ isNew ? '添加 CLI 工具' : `编辑 ${tool?.name}` }}</h2>
            <p class="small faint">{{ isNew ? '保存后立即检测本机是否安装' : tool?.source === 'builtin' ? '内置工具：修改后可随时恢复默认' : tool?.source === 'modified' ? '已修改的内置工具' : '自定义工具' }}</p>
          </div>
        </div>
        <button type="button" class="btn ghost sm" aria-label="关闭" @click="emit('close')"><Icon name="close" /></button>
      </header>

      <div class="sheet-body form">
        <div class="grid2">
          <label class="field">标识
            <input id="ed-id" v-model.trim="f.id" class="mono" :disabled="!isNew" placeholder="my-agent" :aria-invalid="!!err('id')" />
            <span v-if="err('id')" class="err">{{ err('id') }}</span>
          </label>
          <label class="field">名称
            <input id="ed-name" v-model="f.name" placeholder="My Agent" :aria-invalid="!!err('name')" />
            <span v-if="err('name')" class="err">{{ err('name') }}</span>
          </label>
        </div>
        <label class="field">厂商
          <input id="ed-vendor" v-model="f.vendor" placeholder="可选" />
        </label>
        <label class="field">可执行文件名 <span class="help">在 PATH 中依次查找，多个用空格分隔</span>
          <input id="ed-cmd" v-model="f.commands" class="mono" placeholder="my-agent" :aria-invalid="!!err('commands')" />
          <span v-if="err('commands')" class="err">{{ err('commands') }}</span>
        </label>

        <fieldset class="group">
          <legend>派发方式</legend>
          <label class="field">非交互调用参数 <span class="help">{prompt} 会替换为任务说明</span>
            <input id="ed-headless" v-model="f.headless" class="mono" placeholder="-p {prompt}" :aria-invalid="!!err('headless')" />
            <span v-if="err('headless')" class="err">{{ err('headless') }}</span>
          </label>
          <label class="field">用量参数 <span class="help">让工具输出含 token 用量的结果；留空则按字符数估算</span>
            <input id="ed-usage" v-model="f.usageArgs" class="mono" placeholder="--output-format json" />
          </label>
          <div class="preview"><span class="faint">派发时执行</span><code>{{ preview }}</code></div>
        </fieldset>

        <fieldset class="group">
          <legend>角色分配依据</legend>
          <label class="field">能力评分 <b class="num">{{ f.capability }}</b>
            <input id="ed-cap" v-model.number="f.capability" type="range" min="0" max="100" />
            <span class="help">评分最高的可派发工具默认担任架构与项目管理</span>
          </label>
          <div class="field">擅长方向
            <div class="chips">
              <label v-for="s in STRENGTHS" :key="s" class="chip" :class="{ on: f.strengths.includes(s) }">
                <input type="checkbox" :value="s" v-model="f.strengths" />{{ strengthName[s] }}
              </label>
            </div>
          </div>
        </fieldset>

        <details class="group adv">
          <summary>高级</summary>
          <label class="field">版本参数
            <input id="ed-ver" v-model="f.versionArgs" class="mono" placeholder="--version" />
          </label>
          <label class="field">额外安装路径 <span class="help">不在 PATH 中时填写，每行一个</span>
            <textarea id="ed-paths" v-model="f.paths" class="mono" rows="3" placeholder="~/.local/bin/my-agent"></textarea>
          </label>
        </details>

        <label class="toggle">
          <span class="switch"><input type="checkbox" :checked="!f.disabled" @change="f.disabled = !($event.target as HTMLInputElement).checked" /><span></span></span>
          <span><b>参与协同</b><small class="faint">关闭后保留在清单中，但不会被分配角色和派发任务</small></span>
        </label>
      </div>

      <footer class="sheet-foot">
        <span class="small err">{{ serverError }}</span>
        <span class="actions">
          <button type="button" class="btn" @click="emit('close')">取消</button>
          <button type="submit" class="btn primary" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </span>
      </footer>
    </form>
  </div>
</template>

<style scoped>
.form { display: grid; gap: 16px; align-content: start; }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.group { border: 1px solid var(--line); border-radius: var(--r); padding: 14px; margin: 0; display: grid; gap: 14px; }
.group legend, .group summary { font: 600 11px var(--font-mono); letter-spacing: 0.08em; text-transform: uppercase; color: var(--ink-3); padding: 0 6px; }
.adv summary { cursor: pointer; padding: 0; }
.adv[open] summary { margin-bottom: 4px; }
.preview { display: grid; gap: 6px; font-size: 12px; }
.preview code { display: block; padding: 10px 12px; border-radius: var(--r-sm); background: var(--rail); color: var(--rail-ink); overflow-x: auto; white-space: nowrap; }
input[type=range] { accent-color: var(--accent); width: 100%; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip { display: inline-flex; align-items: center; height: 30px; padding: 0 12px; border-radius: 999px; border: 1px solid var(--line-strong); cursor: pointer; color: var(--ink-2); font-weight: 500; }
.chip input { position: absolute; opacity: 0; pointer-events: none; }
.chip.on { background: var(--accent-soft); border-color: var(--accent); color: var(--accent); }
.chip:focus-within { box-shadow: var(--ring); }
.toggle { display: flex; gap: 12px; align-items: center; padding: 12px; border-radius: var(--r); background: var(--sunken); }
.toggle > span:last-child { display: grid; }
.err { color: var(--bad); }
@media (max-width: 520px) { .grid2 { grid-template-columns: 1fr; } }
</style>
