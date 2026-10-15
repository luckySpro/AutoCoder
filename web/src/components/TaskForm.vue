<script setup lang="ts">
// 新建 / 修改任务：标题、说明、角色、负责工具、依赖、修改范围、验收标准与验证命令
import { computed, reactive, ref } from 'vue'
import { createTask, roleName, store, toolName, updateTask } from '../api'
import type { Project, RoleId, Task, TaskInput } from '../types'
import Icon from './Icon.vue'

const props = defineProps<{ project: Project; task?: Task }>()
const emit = defineEmits<{ close: []; saved: [id: string] }>()

const lines = (a?: string[]) => (a ?? []).join('\n')
const split = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)

const f = reactive({
  title: props.task?.title ?? '',
  description: props.task?.description ?? '',
  kind: props.task?.kind ?? ('feature' as Task['kind']),
  role: props.task?.role ?? ('developer' as RoleId),
  assignee: props.task?.assignee ?? '',
  dependsOn: [...(props.task?.dependsOn ?? [])],
  scope: lines(props.task?.scope),
  acceptance: lines(props.task?.acceptance),
  verify: lines(props.task?.verify),
})
const error = ref('')
const saving = ref(false)

const roles: RoleId[] = ['developer', 'frontend', 'tester', 'reviewer', 'lead']
const kinds: { id: Task['kind']; label: string }[] = [
  { id: 'feature', label: '功能' }, { id: 'ui', label: '界面' }, { id: 'test', label: '测试' }, { id: 'infra', label: '基础设施' },
]
// 承担该角色的工具排在前面
const tools = computed(() => {
  const ok = store.tools.filter((t) => t.installed && !t.disabled && t.headless?.length)
  const has = (id: string) => store.assignments.find((a) => a.toolId === id)?.roles.includes(f.role)
  return [...ok].sort((a, b) => Number(!!has(b.id)) - Number(!!has(a.id)))
})
const autoPick = computed(() => store.assignments.find((a) => a.roles.includes(f.role))?.toolId)
const others = computed(() => props.project.tasks.filter((t) => t.id !== props.task?.id))

function toggleDep(id: string) {
  const i = f.dependsOn.indexOf(id)
  if (i >= 0) f.dependsOn.splice(i, 1)
  else f.dependsOn.push(id)
}

async function submit() {
  error.value = ''
  if (!f.title.trim()) {
    error.value = '请填写任务标题'
    return
  }
  const input: TaskInput = {
    title: f.title.trim(), description: f.description.trim(), kind: f.kind, role: f.role, assignee: f.assignee,
    dependsOn: f.dependsOn, scope: split(f.scope), acceptance: split(f.acceptance), verify: split(f.verify),
  }
  saving.value = true
  try {
    if (props.task) {
      await updateTask(props.project.id, props.task.id, input)
      emit('saved', props.task.id)
    } else {
      const t = await createTask(props.project.id, input)
      emit('saved', t.id)
    }
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="scrim" @click.self="emit('close')" @keydown.esc="emit('close')">
    <form class="sheet" role="dialog" aria-modal="true" aria-labelledby="task-title" @submit.prevent="submit">
      <header class="sheet-head">
        <div>
          <h2 id="task-title">{{ task ? `修改 ${task.id}` : '新建任务' }}</h2>
          <p class="small muted">依赖都合并后自动派发；工具只在自己的分支和 worktree 里改代码，提交、验证和合并由 AutoCoder 完成。</p>
        </div>
        <button type="button" class="btn ghost sm" aria-label="关闭" @click="emit('close')"><Icon name="close" /></button>
      </header>

      <div class="sheet-body body">
        <label class="field">标题
          <input v-model="f.title" placeholder="例如：订单 API 与状态机" autofocus />
        </label>
        <label class="field">说明 <span class="help">交给工具的任务描述，越具体越好</span>
          <textarea v-model="f.description" rows="4" placeholder="要做什么、接口约定、注意事项"></textarea>
        </label>

        <div class="grid3">
          <label class="field">类型
            <select v-model="f.kind"><option v-for="k in kinds" :key="k.id" :value="k.id">{{ k.label }}</option></select>
          </label>
          <label class="field">角色
            <select v-model="f.role"><option v-for="r in roles" :key="r" :value="r">{{ roleName(r) }}</option></select>
          </label>
          <label class="field">负责工具
            <select v-model="f.assignee">
              <option value="">按角色自动选择{{ autoPick ? `（${toolName(autoPick)}）` : '' }}</option>
              <option v-for="t in tools" :key="t.id" :value="t.id">{{ t.name }}</option>
            </select>
          </label>
        </div>

        <div v-if="others.length" class="field">依赖 <span class="help">这些任务合并后才开始</span>
          <div class="deps">
            <button v-for="t in others" :key="t.id" type="button" class="chip" :class="{ on: f.dependsOn.includes(t.id) }" :aria-pressed="f.dependsOn.includes(t.id)" @click="toggleDep(t.id)">
              <b class="mono">{{ t.id }}</b> {{ t.title }}
            </button>
          </div>
        </div>

        <div class="grid2">
          <label class="field">验收标准 <span class="help">每行一条</span>
            <textarea v-model="f.acceptance" rows="4" placeholder="订单状态：待支付 → 已支付 → 已发货 → 完成"></textarea>
          </label>
          <label class="field">验证命令 <span class="help">每行一条，由 AutoCoder 运行；项目测试命令会自动加上</span>
            <textarea v-model="f.verify" class="mono" rows="4" placeholder="go test ./internal/order/..."></textarea>
          </label>
        </div>
        <label class="field">修改范围 <span class="help">每行一个路径或通配符，范围外的改动会被撤回；留空表示不限</span>
          <textarea v-model="f.scope" class="mono" rows="2" placeholder="internal/order/**"></textarea>
        </label>
      </div>

      <footer class="sheet-foot">
        <span class="small" :class="error ? 'err' : 'faint'">{{ error || (f.dependsOn.length ? `等 ${f.dependsOn.join('、')} 合并后开始` : '保存后进入派发队列') }}</span>
        <span class="actions">
          <button type="button" class="btn" @click="emit('close')">取消</button>
          <button type="submit" class="btn primary" :disabled="saving">{{ saving ? '保存中…' : task ? '保存' : '创建任务' }}</button>
        </span>
      </footer>
    </form>
  </div>
</template>

<style scoped>
.body { display: grid; gap: 14px; }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.grid3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
.deps { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  font: inherit; font-size: 12.5px; padding: 5px 10px; border: 1px solid var(--line-strong); background: var(--surface); color: var(--ink-2);
  border-radius: var(--r-sm); cursor: pointer; text-align: left;
}
.chip b { font-size: 11.5px; }
.chip.on { border-color: var(--ink); background: var(--ink); color: var(--canvas); }
.err { color: var(--bad); }
.actions { display: flex; gap: 8px; }
select { height: 36px; }
@media (max-width: 620px) { .grid2, .grid3 { grid-template-columns: 1fr; } }
</style>
