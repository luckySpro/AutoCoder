<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { fetchIdentity, fetchLocalRepos, fetchRepos, store, toolName } from '../api'
import type { GitIdentity, GitRepo, LocalRepo, Project } from '../types'
import Icon from './Icon.vue'

const emit = defineEmits<{ close: []; added: [id: string] }>()

type Source = 'github' | 'local' | 'new'
const source = ref<Source>('github')
const identity = ref<GitIdentity>()
const repos = ref<GitRepo[]>([])
const reposError = ref('')
const reposLoading = ref(true)
const locals = ref<LocalRepo[]>([])
const q = ref('')

const form = reactive({
  repo: '' as string, // owner/name
  pasted: '',
  cloneTo: '',
  localPath: '',
  name: '',
  parent: '~/code',
  baseBranch: 'main',
  createRemote: true,
  requirement: '',
})
const error = ref('')

onMounted(async () => {
  identity.value = await fetchIdentity().catch(() => undefined)
  fetchLocalRepos().then((r) => (locals.value = r)).catch(() => {})
  if (!identity.value?.ghLoggedIn) {
    reposLoading.value = false
    return
  }
  try {
    repos.value = await fetchRepos()
  } catch (e) {
    reposError.value = (e as Error).message
  } finally {
    reposLoading.value = false
  }
})

const ghReady = computed(() => identity.value?.ghLoggedIn)
const filtered = computed(() => {
  const k = q.value.trim().toLowerCase()
  return repos.value.filter((r) => !k || r.nameWithOwner.toLowerCase().includes(k) || r.description?.toLowerCase().includes(k))
})
const selectedRepo = computed(() => repos.value.find((r) => r.nameWithOwner === form.repo))

// 选中仓库后自动填写克隆目录与主分支
watch(() => form.repo, (v) => {
  if (!v) return
  form.cloneTo = `~/code/${v.split('/')[1]}`
  form.baseBranch = selectedRepo.value?.defaultBranch || 'main'
  form.pasted = ''
})
watch(() => form.pasted, (v) => {
  if (!v) return
  form.repo = ''
  const name = v.replace(/\.git$/, '').split(/[/:]/).pop() || ''
  form.cloneTo = `~/code/${name}`
})

function pickLocal(r: LocalRepo) {
  form.localPath = r.path
  form.baseBranch = r.branch || 'main'
}

const projectName = computed(() => {
  if (source.value === 'github') return (form.repo || form.pasted).replace(/\.git$/, '').split(/[/:]/).pop() || ''
  if (source.value === 'local') return form.localPath.replace(/\/$/, '').split('/').pop() || ''
  return form.name
})
const projectPath = computed(() =>
  source.value === 'github' ? form.cloneTo : source.value === 'local' ? form.localPath : `${form.parent.replace(/\/$/, '')}/${form.name || '<名称>'}`,
)

const lead = computed(() => store.assignments.find((a) => a.roles.includes('lead')))

// 实际要执行的命令，先给用户看
const commands = computed(() => {
  const c: string[] = []
  const p = projectPath.value || '<目录>'
  if (source.value === 'github') {
    if (form.repo) c.push(`gh repo clone ${form.repo} ${p}`)
    else c.push(`git clone ${form.pasted || '<仓库地址>'} ${p}`)
  } else if (source.value === 'new') {
    c.push(`git init -b ${form.baseBranch} ${p}`)
    if (form.createRemote && ghReady.value) c.push(`gh repo create ${identity.value?.ghLogin}/${form.name || '<名称>'} --private --source ${p} --remote origin`)
  }
  c.push(`cd ${p}`)
  c.push('# 写入 .autocoder/project.yaml、plan.md 并提交')
  c.push(`git switch -c ac/integration ${form.baseBranch}`)
  c.push(`# 交给 ${lead.value ? toolName(lead.value.toolId) : '项目管理'} 做架构设计与任务拆分`)
  return c
})

const ready = computed(() => {
  if (source.value === 'github') return !!(form.repo || form.pasted) && !!form.cloneTo
  if (source.value === 'local') return !!form.localPath
  return /^[A-Za-z0-9._-]+$/.test(form.name)
})

function submit() {
  error.value = ''
  if (!ready.value) {
    error.value = source.value === 'new' ? '项目名称只能包含字母、数字、点、下划线和连字符' : '请先选择一个仓库'
    return
  }
  const id = projectName.value
  if (store.projects.some((p) => p.id === id)) {
    error.value = `已经有名为 ${id} 的项目`
    return
  }
  const remote = source.value === 'github'
    ? (selectedRepo.value?.url ?? form.pasted)
    : source.value === 'local' ? locals.value.find((l) => l.path === form.localPath)?.remote
    : form.createRemote && ghReady.value ? `https://github.com/${identity.value?.ghLogin}/${form.name}` : undefined
  const p: Project = {
    id, name: id, path: projectPath.value, remote, baseBranch: form.baseBranch, integrationBranch: 'ac/integration',
    description: form.requirement || selectedRepo.value?.description || '新项目', updatedAt: '刚刚',
    tasks: [{ id: 'T1', title: '架构设计与任务拆分', kind: 'infra', role: 'lead', assignee: lead.value?.toolId ?? '', status: 'ready', dependsOn: [], branch: 'ac/T1-architecture', progress: 0, commits: 0 }],
    reports: [],
  }
  store.projects.unshift(p)
  emit('added', id)
}

const ago = (iso: string) => {
  const d = (Date.now() - new Date(iso).getTime()) / 86400000
  return d < 1 ? '今天' : d < 2 ? '昨天' : d < 30 ? `${Math.floor(d)} 天前` : new Date(iso).toLocaleDateString()
}

const sources: { id: Source; label: string; hint: string; icon: string }[] = [
  { id: 'github', label: 'GitHub 仓库', hint: '从你的账号里选', icon: 'github' },
  { id: 'local', label: '本机仓库', hint: '已经 clone 过的', icon: 'folder' },
  { id: 'new', label: '新建仓库', hint: '从零开始', icon: 'plus' },
]
</script>

<template>
  <div class="scrim" @click.self="emit('close')" @keydown.esc="emit('close')">
    <form class="sheet" role="dialog" aria-modal="true" aria-labelledby="add-title" @submit.prevent="submit">
      <header class="sheet-head">
        <div>
          <h2 id="add-title">添加项目</h2>
          <p class="who small muted" v-if="identity">
            <span>git：<b>{{ identity.gitName || '未设置' }}</b> <span class="faint">&lt;{{ identity.gitEmail || '未设置邮箱' }}&gt;</span></span>
            <span v-if="identity.ghLoggedIn" class="pill ok">GitHub · {{ identity.ghLogin }}</span>
            <span v-else-if="identity.ghInstalled" class="pill warn">gh 未登录</span>
            <span v-else class="pill">未安装 gh</span>
          </p>
        </div>
        <button type="button" class="btn ghost sm" aria-label="关闭" @click="emit('close')"><Icon name="close" /></button>
      </header>

      <div class="sheet-body body">
        <div class="sources" role="tablist" aria-label="项目来源">
          <button v-for="s in sources" :key="s.id" type="button" role="tab" :aria-selected="source === s.id" class="src" @click="source = s.id">
            <Icon :name="s.icon" />
            <span><b>{{ s.label }}</b><small>{{ s.hint }}</small></span>
          </button>
        </div>

        <div class="pane">
          <!-- GitHub -->
          <template v-if="source === 'github'">
            <template v-if="ghReady">
              <label class="search">
                <Icon name="search" />
                <input v-model="q" type="search" placeholder="搜索仓库" aria-label="搜索仓库" />
              </label>
              <div class="list" role="listbox" aria-label="仓库">
                <p v-if="reposLoading" class="empty">正在读取 {{ identity?.ghLogin }} 的仓库…</p>
                <p v-else-if="reposError" class="empty">{{ reposError }}</p>
                <p v-else-if="!filtered.length" class="empty">没有匹配"{{ q }}"的仓库</p>
                <button v-for="r in filtered" :key="r.nameWithOwner" type="button" role="option" :aria-selected="form.repo === r.nameWithOwner"
                  class="item" @click="form.repo = r.nameWithOwner">
                  <span class="ttl"><Icon :name="r.isPrivate ? 'lock' : 'github'" :size="15" /><span class="mono">{{ r.nameWithOwner }}</span></span>
                  <span class="desc">{{ r.description || '没有描述' }}</span>
                  <span class="meta"><span class="branch">{{ r.defaultBranch }}</span>{{ ago(r.updatedAt) }}</span>
                </button>
              </div>
            </template>
            <div v-else class="gh-empty">
              <Icon name="github" :size="28" />
              <div>
                <b>{{ identity?.ghInstalled ? 'GitHub CLI 还没有登录' : '没有检测到 GitHub CLI（gh）' }}</b>
                <p class="muted small">在终端执行 <code>gh auth login</code> 登录后，这里会列出你账号下的仓库，选中即可添加。也可以直接粘贴仓库地址。</p>
              </div>
            </div>
            <div class="grid2">
              <label class="field">或粘贴仓库地址
                <input v-model.trim="form.pasted" class="mono" placeholder="git@github.com:you/repo.git" />
              </label>
              <label class="field">克隆到
                <input v-model.trim="form.cloneTo" class="mono" placeholder="~/code/repo" />
              </label>
            </div>
          </template>

          <!-- 本机 -->
          <template v-else-if="source === 'local'">
            <div class="list" role="listbox" aria-label="本机仓库">
              <p v-if="!locals.length" class="empty">在 ~/code、~/projects 等常用目录下没有找到仓库，可以在下方直接填写目录。</p>
              <button v-for="r in locals" :key="r.path" type="button" role="option" :aria-selected="form.localPath === r.path" class="item" @click="pickLocal(r)">
                <span class="ttl"><Icon name="folder" :size="15" /><b>{{ r.name }}</b></span>
                <span class="desc mono">{{ r.path }}</span>
                <span class="meta"><span v-if="r.branch" class="branch">{{ r.branch }}</span>{{ r.remote ? r.remote.replace(/^.*[:/]([^/]+\/[^/]+?)(\.git)?$/, '$1') : '无远程' }}</span>
              </button>
            </div>
            <label class="field">仓库目录
              <input v-model.trim="form.localPath" class="mono" placeholder="~/code/my-project" />
            </label>
          </template>

          <!-- 新建 -->
          <template v-else>
            <div class="grid2">
              <label class="field">项目名称
                <input v-model.trim="form.name" class="mono" placeholder="shop-admin" :aria-invalid="!!form.name && !ready" />
              </label>
              <label class="field">父目录
                <input v-model.trim="form.parent" class="mono" />
              </label>
            </div>
            <label class="check" :class="{ off: !ghReady }">
              <span class="switch"><input type="checkbox" v-model="form.createRemote" :disabled="!ghReady" /><span></span></span>
              <span>
                <b>同时在 GitHub 创建私有仓库</b>
                <small class="faint">{{ ghReady ? `创建在 ${identity?.ghLogin} 账号下，并设为 origin` : '需要先登录 gh' }}</small>
              </span>
            </label>
          </template>

          <div class="grid2">
            <label class="field">主分支
              <input v-model.trim="form.baseBranch" class="mono" />
            </label>
            <div class="field">集成分支
              <span class="branch fixed">ac/integration</span>
            </div>
          </div>
          <label class="field">需求描述 <span class="help">交给 {{ lead ? toolName(lead.toolId) : '项目管理' }} 拆分成任务，可稍后再填</span>
            <textarea v-model="form.requirement" rows="2" placeholder="例如：商城后台，包含商品、订单、会员管理"></textarea>
          </label>
          <details class="cmds">
            <summary>将执行的命令</summary>
            <pre><code>{{ commands.join('\n') }}</code></pre>
          </details>
        </div>
      </div>

      <footer class="sheet-foot">
        <span class="small" :class="error ? 'err' : 'faint'">{{ error || (projectName ? `项目：${projectName}` : '选择或填写一个仓库') }}</span>
        <span class="actions">
          <button type="button" class="btn" @click="emit('close')">取消</button>
          <button type="submit" class="btn primary" :disabled="!ready">添加项目</button>
        </span>
      </footer>
    </form>
  </div>
</template>

<style scoped>
.who { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 6px; }
.body { display: grid; grid-template-columns: 200px minmax(0, 1fr); gap: 20px; }
.sources { display: grid; gap: 6px; align-content: start; }
.src {
  display: flex; gap: 10px; align-items: center; text-align: left; padding: 10px 12px; border-radius: var(--r);
  border: 1px solid var(--line); background: var(--surface); color: var(--ink-2); cursor: pointer; font: inherit;
}
.src span { display: grid; line-height: 1.3; }
.src b { color: var(--ink); font-weight: 600; font-size: 13.5px; }
.src small { font-size: 12px; color: var(--ink-3); }
.src[aria-selected=true] { border-color: var(--accent); background: var(--accent-soft); color: var(--accent); }
.pane { display: grid; gap: 14px; min-width: 0; align-content: start; }
.search { position: relative; display: block; }
.search svg { position: absolute; left: 11px; top: 50%; transform: translateY(-50%); color: var(--ink-3); }
.search input { padding-left: 36px; }
.list { border: 1px solid var(--line); border-radius: var(--r); max-height: 300px; overflow-y: auto; background: var(--sunken); }
.item {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 2px 12px; width: 100%; text-align: left;
  padding: 10px 12px; border: 0; border-bottom: 1px solid var(--line); background: transparent; color: var(--ink); font: inherit; cursor: pointer;
}
.item:last-child { border-bottom: 0; }
.item:hover { background: var(--surface); }
.item[aria-selected=true] { background: var(--accent-soft); box-shadow: inset 3px 0 0 var(--accent); }
.ttl { display: flex; align-items: center; gap: 7px; min-width: 0; }
.ttl .mono { font-size: 13px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ttl svg { color: var(--ink-3); flex: none; }
.desc { grid-column: 1; color: var(--ink-2); font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.meta { grid-row: 1 / span 2; grid-column: 2; display: grid; justify-items: end; gap: 4px; color: var(--ink-3); font-size: 12px; align-content: center; }
.empty { padding: 24px 16px; text-align: center; color: var(--ink-3); font-size: 13px; }
.gh-empty { display: flex; gap: 14px; padding: 18px; border: 1px dashed var(--line-strong); border-radius: var(--r); color: var(--ink-2); }
.gh-empty b { color: var(--ink); }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.check { display: flex; gap: 12px; align-items: center; padding: 12px; border-radius: var(--r); background: var(--sunken); }
.check span:last-child { display: grid; }
.check.off { opacity: 0.6; }
.fixed { height: 38px; padding: 0 11px; align-self: stretch; }
.cmds summary { cursor: pointer; color: var(--ink-2); font-size: 13px; }
.cmds pre { margin: 8px 0 0; padding: 12px 14px; border-radius: var(--r); background: var(--rail); color: var(--rail-ink); overflow-x: auto; font-size: 12px; line-height: 1.7; }
.err { color: var(--bad); }
@media (max-width: 720px) {
  .body { grid-template-columns: 1fr; }
  .sources { grid-auto-flow: column; grid-auto-columns: 1fr; }
  .src small { display: none; }
  .grid2 { grid-template-columns: 1fr; }
}
</style>
