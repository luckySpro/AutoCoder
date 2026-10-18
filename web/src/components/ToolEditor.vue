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
  helpArgs: join(props.tool?.helpArgs ?? ['--help']),
  headless: join(props.tool?.headless ?? ['-p', '{prompt}']),
  usageArgs: join(props.tool?.usageArgs),
  autoArgs: join(props.tool?.autoArgs),
  fullArgs: join(props.tool?.fullArgs),
  access: props.tool?.access === 'standard' ? 'standard' : 'full',
  streamArgs: join(props.tool?.streamArgs),
  readonlyArgs: join(props.tool?.readonlyArgs),
  authErrors: (props.tool?.authErrors ?? []).join('\n'),
  limitErrors: (props.tool?.limitErrors ?? []).join('\n'),
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
// 与后端 Tool.Args 一致：无人值守与事件流参数插在提示词之前（提示词前紧挨的选项如 -p 一起后移）
const preview = computed(() => {
  const h = split(f.headless)
  const perm = f.access !== 'standard' && split(f.fullArgs).length ? split(f.fullArgs) : split(f.autoArgs)
  const extra = [...perm, ...(split(f.streamArgs).length ? split(f.streamArgs) : split(f.usageArgs))]
  let at = h.indexOf('{prompt}')
  if (at < 0) at = h.length
  else if (at > 0 && h[at - 1].startsWith('-')) at--
  const args = [...h.slice(0, at), ...extra, ...h.slice(at)].map((a) => (a === '{prompt}' ? '"<任务说明>"' : a))
  return [firstCmd.value, ...args].join(' ')
})
const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
const opt = (a: string[]) => (a.length ? a : undefined)

function toSpec(): ToolSpec {
  const ps = lines(f.paths)
  // 每个系统只检查自己的路径；界面不区分系统，三处写同一份，不存在的路径会被忽略
  const paths = ps.length ? { darwin: ps, linux: ps, windows: ps } : undefined
  return {
    id: f.id, name: f.name.trim(), vendor: f.vendor.trim(), commands: split(f.commands), paths,
    versionArgs: split(f.versionArgs), helpArgs: opt(split(f.helpArgs)), headless: split(f.headless), usageArgs: opt(split(f.usageArgs)),
    autoArgs: opt(split(f.autoArgs)), fullArgs: opt(split(f.fullArgs)), access: f.access === 'standard' ? 'standard' : undefined, streamArgs: opt(split(f.streamArgs)), readonlyArgs: opt(split(f.readonlyArgs)),
    authErrors: opt(lines(f.authErrors)), limitErrors: opt(lines(f.limitErrors)),
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
          <label class="field">运行权限 <span class="help">最高权限下工具可以直接执行命令、联网安装依赖，不会停下来等批准</span>
            <select v-model="f.access">
              <option value="full">最高权限（默认）</option>
              <option value="standard">只改文件，命令要批准</option>
            </select>
          </label>
          <div class="grid2">
            <label class="field">最高权限参数 <span class="help">权限选「最高权限」时使用</span>
              <input v-model="f.fullArgs" class="mono" placeholder="--permission-mode bypassPermissions" />
            </label>
            <label class="field">只改文件参数 <span class="help">权限选「只改文件」，或没填最高权限参数时使用</span>
              <input v-model="f.autoArgs" class="mono" placeholder="--permission-mode acceptEdits" />
            </label>
          </div>
          <div class="grid2">
            <label class="field">事件流参数 <span class="help">实时查看过程用</span>
              <input v-model="f.streamArgs" class="mono" placeholder="--output-format stream-json" />
            </label>
            <label class="field">用量参数 <span class="help">没有事件流时使用；都留空则估算</span>
              <input id="ed-usage" v-model="f.usageArgs" class="mono" placeholder="--output-format json" />
            </label>
          </div>
          <div class="preview"><span class="faint">派发时执行</span><code>{{ preview }}</code></div>
        </fieldset>

        <fieldset class="group">
          <legend>只读与异常识别</legend>
          <label class="field">只读参数 <span class="help">规划与评审时代替无人值守参数</span>
            <input v-model="f.readonlyArgs" class="mono" placeholder="--permission-mode plan" />
          </label>
          <div class="grid2">
            <label class="field">"未登录"关键字 <span class="help">每行一个，不分大小写</span>
              <textarea v-model="f.authErrors" class="mono" rows="3" placeholder="not logged in"></textarea>
            </label>
            <label class="field">"限流 / 额度用完"关键字
              <textarea v-model="f.limitErrors" class="mono" rows="3" placeholder="rate limit&#10;429"></textarea>
            </label>
          </div>
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
          <label class="field">帮助参数 <span class="help">检测时读它的帮助，核对上面的参数当前版本还认不认识；留空不核对</span>
            <input id="ed-help" v-model="f.helpArgs" class="mono" placeholder="--help" />
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
