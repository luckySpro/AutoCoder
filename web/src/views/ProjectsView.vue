<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { roleName, store, toolName } from '../api'
import type { Project } from '../types'

const open = ref(false)
const form = reactive({
  name: '',
  mode: 'new' as 'new' | 'clone' | 'existing',
  path: '~/code/',
  remote: '',
  baseBranch: 'main',
  requirement: '',
})
const errors = reactive({ name: '', remote: '' })

function stats(p: Project) {
  const done = p.tasks.filter((t) => t.status === 'merged').length
  const blocked = p.tasks.filter((t) => t.status === 'blocked').length
  return { done, total: p.tasks.length, blocked, pct: p.tasks.length ? Math.round((done / p.tasks.length) * 100) : 0 }
}

const dir = computed(() => (form.mode === 'existing' ? form.path : `${form.path.replace(/\/$/, '')}/${form.name || '<name>'}`))

// 创建项目时实际要执行的 git 操作，界面上先预览
const commands = computed(() => {
  const c: string[] = []
  if (form.mode === 'new') c.push(`git init -b ${form.baseBranch} ${dir.value}`)
  if (form.mode === 'clone') c.push(`git clone ${form.remote || '<remote>'} ${dir.value}`)
  c.push(`cd ${dir.value}`)
  c.push('mkdir -p .autocoder/tasks .autocoder/reports  # 写入 project.yaml 与 plan.md')
  c.push(`git add .autocoder && git commit -m "chore(autocoder): 初始化项目"`)
  c.push(`git branch ac/integration ${form.baseBranch}`)
  if (form.mode === 'new' && form.remote) c.push(`git remote add origin ${form.remote}`)
  const lead = store.assignments.find((a) => a.roles.includes('lead'))
  c.push(`# 交给 ${lead ? toolName(lead.toolId) : '项目管理'} 进行架构设计与任务拆分（最多同时运行 ${store.settings.concurrency} 个工具）`)
  return c
})

function submit() {
  errors.name = /^[a-zA-Z0-9._-]+$/.test(form.name) ? '' : '只能包含字母、数字、点、下划线和连字符'
  errors.remote = form.mode === 'clone' && !form.remote ? '克隆模式需要填写仓库地址' : ''
  if (errors.name || errors.remote) return
  store.projects.unshift({
    id: form.name, name: form.name, path: dir.value, remote: form.remote || undefined,
    baseBranch: form.baseBranch, integrationBranch: 'ac/integration',
    description: form.requirement || '新项目', updatedAt: new Date().toLocaleString(),
    tasks: [{ id: 'T1', title: '架构设计与任务拆分', kind: 'infra', role: 'lead', assignee: store.assignments.find((a) => a.roles.includes('lead'))?.toolId ?? '', status: 'ready', dependsOn: [], branch: 'ac/T1-architecture', progress: 0, commits: 0 }],
    reports: [],
  })
  open.value = false
  Object.assign(form, { name: '', remote: '', requirement: '' })
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>项目</h1>
        <p>每个项目就是一个 git 仓库；任务对应分支，进度对应提交，集成对应合并。</p>
      </div>
      <button class="btn primary" @click="open = true">＋ 新建项目</button>
    </div>

    <div class="grid projects">
      <RouterLink v-for="p in store.projects" :key="p.id" :to="`/projects/${p.id}`" class="card project">
        <div class="row between">
          <strong>{{ p.name }}</strong>
          <span v-if="stats(p).blocked" class="badge danger">{{ stats(p).blocked }} 个受阻</span>
        </div>
        <p class="muted desc">{{ p.description }}</p>
        <div class="mono faint">{{ p.path }}</div>
        <div class="row branch">
          <span class="badge">⎇ {{ p.baseBranch }}</span>
          <span class="badge">⎇ {{ p.integrationBranch }}</span>
        </div>
        <div class="progress" :aria-label="`完成 ${stats(p).pct}%`"><span :style="{ width: stats(p).pct + '%' }"></span></div>
        <div class="row between small">
          <span class="muted">{{ stats(p).done }}/{{ stats(p).total }} 个任务已合并</span>
          <span class="faint">{{ p.updatedAt }}</span>
        </div>
      </RouterLink>
    </div>

    <div v-if="open" class="mask" @click.self="open = false">
      <form class="dialog card" role="dialog" aria-modal="true" aria-labelledby="new-title" @submit.prevent="submit">
        <h2 id="new-title">新建项目</h2>
        <div class="seg" role="radiogroup" aria-label="来源">
          <label v-for="m in [{ v: 'new', l: '新仓库' }, { v: 'clone', l: '克隆远程' }, { v: 'existing', l: '已有目录' }]" :key="m.v" :class="{ on: form.mode === m.v }">
            <input type="radio" v-model="form.mode" :value="m.v" />{{ m.l }}
          </label>
        </div>
        <div class="fields">
          <label class="field">项目名称 *
            <input v-model.trim="form.name" required placeholder="shop-admin" />
            <span v-if="errors.name" class="err">{{ errors.name }}</span>
          </label>
          <label class="field">{{ form.mode === 'existing' ? '仓库目录' : '父目录' }}
            <input v-model.trim="form.path" />
          </label>
          <label class="field">远程仓库{{ form.mode === 'clone' ? ' *' : '（可选）' }}
            <input v-model.trim="form.remote" placeholder="git@github.com:you/repo.git" />
            <span v-if="errors.remote" class="err">{{ errors.remote }}</span>
          </label>
          <label class="field">主分支
            <input v-model.trim="form.baseBranch" />
          </label>
          <label class="field wide">需求描述（交给项目管理拆分）
            <textarea v-model="form.requirement" rows="3" placeholder="例如：做一个商城后台，包含商品、订单、会员管理"></textarea>
          </label>
        </div>
        <div class="preview">
          <div class="faint small">将执行</div>
          <pre><code>{{ commands.join('\n') }}</code></pre>
        </div>
        <p class="faint small">
          当前分工：
          <span v-for="a in store.assignments" :key="a.toolId">{{ a.toolName }}（{{ a.roles.map(roleName).join('、') }}） </span>
        </p>
        <div class="row end">
          <button type="button" class="btn" @click="open = false">取消</button>
          <button type="submit" class="btn primary">创建</button>
        </div>
      </form>
    </div>
  </section>
</template>

<style scoped>
.project { display: grid; gap: 8px; color: var(--text); transition: border-color 0.15s; }
.project:hover, .project:focus-visible { border-color: var(--primary); }
.between { justify-content: space-between; }
.desc { margin: 0; }
.small { font-size: 12.5px; }
.progress { height: 6px; border-radius: 3px; background: var(--surface-2); overflow: hidden; }
.progress span { display: block; height: 100%; background: var(--success); }
.mask { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.4); display: grid; place-items: center; padding: 16px; z-index: 10; }
.dialog { width: min(640px, 100%); max-height: calc(100vh - 32px); overflow-y: auto; display: grid; gap: 14px; }
.dialog h2 { margin: 0; }
.seg { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-sm); overflow: hidden; width: fit-content; }
.seg label { padding: 6px 14px; cursor: pointer; font-size: 13px; }
.seg label.on { background: var(--primary-soft); color: var(--primary); }
.seg input { position: absolute; opacity: 0; pointer-events: none; }
.fields { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.fields .wide { grid-column: 1 / -1; }
.err { color: var(--danger); font-size: 12px; }
.preview pre { margin: 4px 0 0; background: var(--surface-2); padding: 10px 12px; border-radius: var(--radius-sm); overflow-x: auto; font-size: 12px; }
.end { justify-content: flex-end; }
@media (max-width: 560px) { .fields { grid-template-columns: 1fr; } }
</style>
