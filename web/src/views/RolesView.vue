<script setup lang="ts">
import { computed, ref } from 'vue'
import { agent } from '../agents'
import { MAX_CONCURRENCY, resetAssignments, saveAssignments, store } from '../api'
import AgentMark from '../components/AgentMark.vue'
import Icon from '../components/Icon.vue'
import type { RoleId } from '../types'

const saved = ref(false)

const has = (toolId: string, role: RoleId) =>
  store.assignments.find((a) => a.toolId === toolId)?.roles.includes(role) ?? false

function toggle(toolId: string, role: RoleId) {
  const a = store.assignments.find((x) => x.toolId === toolId)
  if (!a) return
  if (role === 'lead') {
    for (const x of store.assignments) x.roles = x.roles.filter((r) => r !== 'lead')
    a.roles.unshift('lead')
  } else if (a.roles.includes(role)) {
    a.roles = a.roles.filter((r) => r !== role)
  } else {
    a.roles.push(role)
  }
  saved.value = false
}

const uncovered = computed(() => store.roles.filter((r) => !store.assignments.some((a) => a.roles.includes(r.id))))
const lead = computed(() => store.assignments.find((a) => a.roles.includes('lead')))
const cap = (id: string) => store.tools.find((t) => t.id === id)?.capability ?? 0

function setSlots(n: number) {
  store.settings.concurrency = n
  saved.value = false
}

function save() {
  saveAssignments()
  saved.value = true
}
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <div class="eyebrow">分工</div>
        <h1>角色与运行</h1>
        <p class="lede">只有一个工具时它承担全部角色；有多个时，能力评分最高的担任架构与项目管理，其余按擅长方向分到开发、前端、测试和评审。</p>
      </div>
      <div class="actions">
        <button class="btn" @click="resetAssignments()"><Icon name="undo" />恢复推荐</button>
        <button class="btn primary" :disabled="uncovered.length > 0" @click="save"><Icon v-if="saved" name="check" />{{ saved ? '已保存' : '保存' }}</button>
      </div>
    </header>

    <p v-if="!store.assignments.length && !store.loading" class="panel muted">
      还没有可派发的工具。请先到 <RouterLink to="/tools">工具清单</RouterLink> 安装或添加。
    </p>

    <template v-else>
      <div v-if="lead" class="lead" :style="{ '--c': agent(lead.toolId).color }">
        <AgentMark :id="lead.toolId" :size="48" />
        <div>
          <div class="eyebrow">架构与项目管理</div>
          <h2>{{ lead.toolName }}</h2>
          <p class="muted small">负责架构设计，把需求拆成功能与视觉任务，派发给其他工具，汇总进度并把各分支集成起来。</p>
        </div>
        <div class="score"><span class="num">{{ cap(lead.toolId) }}</span><small>能力评分</small></div>
      </div>

      <div v-if="uncovered.length" class="notice" role="alert">
        以下角色没有工具承担：{{ uncovered.map((r) => r.name).join('、') }}。
      </div>

      <div class="panel">
        <div class="table-wrap">
          <table class="matrix">
            <thead>
              <tr>
                <th>工具</th>
                <th v-for="r in store.roles" :key="r.id" :title="r.description">{{ r.name }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="a in store.assignments" :key="a.toolId" :style="{ '--c': agent(a.toolId).color }">
                <td><AgentMark :id="a.toolId" :size="26" label /></td>
                <td v-for="r in store.roles" :key="r.id" class="cell">
                  <label class="cellbox" :class="{ on: has(a.toolId, r.id), lead: r.id === 'lead' }">
                    <input :type="r.id === 'lead' ? 'radio' : 'checkbox'" name="lead" :checked="has(a.toolId, r.id)" @change="toggle(a.toolId, r.id)" />
                    <Icon v-if="has(a.toolId, r.id)" name="check" :size="14" />
                    <span class="sr-only">{{ a.toolName }} 担任 {{ r.name }}</span>
                  </label>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="settings">
        <div class="panel set">
          <div>
            <h2>同时运行的工具数</h2>
            <p class="muted small">同一时间最多派发几个任务进程。每个工具同时只跑一个任务，多出的任务排队。默认 3，最多 {{ MAX_CONCURRENCY }}。</p>
          </div>
          <div class="slotpick" role="radiogroup" aria-label="同时运行的工具数">
            <button v-for="n in MAX_CONCURRENCY" :key="n" type="button" role="radio" :aria-checked="store.settings.concurrency === n"
              :class="{ on: n <= store.settings.concurrency }" @click="setSlots(n)">{{ n }}</button>
          </div>
        </div>
        <div class="panel set">
          <div>
            <h2>自动合并到主分支</h2>
            <p class="muted small">集成分支的测试通过后，由项目管理直接合并到主分支，不再等你确认。合并说明里附带本次的 token 用量。</p>
          </div>
          <label class="switch big">
            <input type="checkbox" v-model="store.settings.autoMerge" aria-label="自动合并到主分支" @change="saved = false" />
            <span></span>
          </label>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.lead {
  display: grid; grid-template-columns: auto 1fr auto; gap: 18px; align-items: center; padding: 20px 22px;
  border-radius: var(--r-lg); border: 1px solid color-mix(in srgb, var(--c) 40%, var(--line));
  background: linear-gradient(100deg, color-mix(in srgb, var(--c) 12%, var(--surface)), var(--surface) 60%);
}
.lead h2 { font-size: 22px; margin: 4px 0; }
.score { display: grid; justify-items: end; }
.score .num { font: 600 34px/1 var(--font-display); color: var(--c); }
.score small { color: var(--ink-3); font-size: 12px; }
.matrix th:not(:first-child), .cell { text-align: center; }
.cellbox {
  position: relative; display: inline-grid; place-items: center; width: 34px; height: 34px; border-radius: 9px;
  border: 1.5px dashed var(--line-strong); cursor: pointer; color: var(--surface); transition: background 0.12s, border-color 0.12s;
}
.cellbox.lead { border-radius: 50%; }
.cellbox input { position: absolute; inset: 0; opacity: 0; margin: 0; cursor: pointer; }
.cellbox:hover { border-color: var(--c); }
.cellbox.on { background: var(--c); border: 1.5px solid var(--c); }
.cellbox:focus-within { box-shadow: var(--ring); }
.settings { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.set { display: flex; justify-content: space-between; align-items: center; gap: 20px; }
.set p { margin-top: 6px; max-width: 46ch; }
.slotpick { display: flex; gap: 6px; flex: none; }
.slotpick button {
  width: 36px; height: 40px; border-radius: 8px; border: 1.5px dashed var(--line-strong); background: transparent;
  color: var(--ink-3); font: 600 13px var(--font-mono); cursor: pointer;
}
.slotpick button.on { border: 1.5px solid var(--accent); background: var(--accent-soft); color: var(--accent); }
.switch.big { transform: scale(1.15); }
@media (max-width: 900px) {
  .settings { grid-template-columns: 1fr; }
  .lead { grid-template-columns: auto 1fr; }
  .score { display: none; }
  .set { flex-wrap: wrap; }
}
</style>
