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

const saveError = ref('')
async function save() {
  saveError.value = ''
  try {
    await saveAssignments()
    saved.value = true
  } catch (e) {
    saveError.value = (e as Error).message
  }
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
      <div v-if="lead" class="lead-card" :style="{ '--c': agent(lead.toolId).color }">
        <span class="tag mono">LEAD</span>
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
                  <button type="button" class="cellbox" :class="{ on: has(a.toolId, r.id), 'lead-cell': r.id === 'lead' }"
                    :role="r.id === 'lead' ? 'radio' : 'checkbox'" :aria-checked="has(a.toolId, r.id)"
                    :aria-label="`${a.toolName} 担任 ${r.name}`" @click="toggle(a.toolId, r.id)">
                    <Icon v-if="has(a.toolId, r.id)" name="check" :size="14" />
                  </button>
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
        <div class="panel set rules">
          <div>
            <h2>派发规则</h2>
            <p class="muted small">失败后带着原因自动重试；连续没有输出也没有文件变化判定为卡住，结束进程后重试。项目没有测试命令时，交付前总会等你确认。</p>
          </div>
          <div class="nums">
            <label><span>最多尝试</span><input v-model.number="store.settings.maxAttempts" type="number" min="1" max="10" @input="saved = false" /><em>次</em></label>
            <label><span>卡住判定</span><input v-model.number="store.settings.idleMinutes" type="number" min="1" max="120" @input="saved = false" /><em>分钟</em></label>
            <label><span>单次上限</span><input v-model.number="store.settings.timeoutMinutes" type="number" min="5" max="480" @input="saved = false" /><em>分钟</em></label>
          </div>
        </div>
        <div class="panel set">
          <div>
            <h2>计划先给我确认</h2>
            <p class="muted small">lead 把需求拆成任务后，先在项目页展示任务图，你确认、删减或要求重新生成后才开始派发。关闭后计划通过校验就直接开始。</p>
          </div>
          <label class="switch big">
            <input type="checkbox" v-model="store.settings.confirmPlan" aria-label="计划先给我确认" @change="saved = false" />
            <span></span>
          </label>
        </div>
        <div class="panel set">
          <div>
            <h2>合并前评审</h2>
            <p class="muted small">验证通过后，由评审工具在只读模式下看 diff 和验收标准；要求修改时带着问题清单重试。评审尽量交给不是作者的工具。</p>
          </div>
          <label class="switch big">
            <input type="checkbox" v-model="store.settings.review" aria-label="合并前评审" @change="saved = false" />
            <span></span>
          </label>
        </div>
        <div class="panel set">
          <div>
            <h2>暂停派发</h2>
            <p class="muted small">不再启动新任务，正在运行的会跑完。</p>
          </div>
          <label class="switch big">
            <input type="checkbox" v-model="store.settings.paused" aria-label="暂停派发" @change="saved = false" />
            <span></span>
          </label>
        </div>
      </div>
      <p v-if="saveError" class="error" role="alert">{{ saveError }}</p>
    </template>
  </section>
</template>

<style scoped>
.lead-card {
  position: relative; display: grid; grid-template-columns: auto 1fr auto; gap: 18px; align-items: center; padding: 22px 22px 20px;
  border: 1px solid var(--ink); background: var(--surface); box-shadow: inset 6px 0 0 var(--c);
  padding-left: 30px;
}
.lead-card .tag { position: absolute; top: -1px; right: -1px; padding: 3px 8px; background: var(--ink); color: var(--canvas); font-size: 10.5px; font-weight: 700; letter-spacing: 0.14em; }
.lead-card h2 { font-size: 24px; font-weight: 800; margin: 2px 0 6px; }
.lead-card .eyebrow { margin-bottom: 0; }
.score { display: grid; justify-items: end; padding-right: 10px; }
.score .num { font: 700 44px/1 var(--font-mono); color: var(--ink); letter-spacing: -0.04em; }
.score small { color: var(--ink-3); font-size: 11px; font-family: var(--font-mono); letter-spacing: 0.08em; }
.matrix th:not(:first-child), .cell { text-align: center; }
.rules { flex-direction: column; align-items: stretch; }
.nums { display: flex; gap: 14px; flex-wrap: wrap; }
.nums label { display: grid; grid-template-columns: auto 64px auto; align-items: center; gap: 8px; font-size: 13px; }
.nums span { color: var(--ink-3); }
.nums em { font-style: normal; color: var(--ink-3); font-size: 12px; }
.nums input { height: 32px; padding: 0 8px; font: 600 13px var(--font-mono); border: 1px solid var(--line-strong); background: var(--surface); color: var(--ink); border-radius: var(--r-sm); width: 64px; }
.error { color: var(--bad); font-size: 13px; }
.matrix td { padding-block: 12px; }
.matrix td:first-child { padding-left: 14px; box-shadow: inset 3px 0 0 var(--c); }
.cellbox {
  display: inline-grid; place-items: center; width: 30px; height: 30px; padding: 0; vertical-align: middle;
  border: 1px solid var(--line-strong); border-radius: var(--r-sm); background: transparent; cursor: pointer; color: #fff;
  transition: background 0.12s, border-color 0.12s;
}
.cellbox:hover { border-color: var(--ink); background: var(--sunken); }
.cellbox.on { background: var(--c); border-color: var(--c); }
.cellbox.lead-cell { border-style: dashed; }
.cellbox.lead-cell.on { border-style: solid; box-shadow: 0 0 0 2px var(--surface), 0 0 0 3px var(--c); }
.settings { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.set { display: flex; justify-content: space-between; align-items: center; gap: 20px; }
.set h2 { font-size: 15px; }
.set p { margin-top: 6px; max-width: 46ch; }
.slotpick { display: flex; flex: none; border: 1px solid var(--ink); }
.slotpick button {
  width: 38px; height: 38px; border: 0; border-left: 1px solid var(--ink); background: transparent;
  color: var(--ink-3); font: 700 13px var(--font-mono); cursor: pointer;
}
.slotpick button:first-child { border-left: 0; }
.slotpick button.on { background: var(--ink); color: var(--canvas); }
.slotpick button[aria-checked=true] { background: var(--accent); color: var(--accent-ink); }
.switch.big { transform: scale(1.15); }
@media (max-width: 900px) {
  .settings { grid-template-columns: 1fr; }
  .lead-card { grid-template-columns: auto 1fr; }
  .score { display: none; }
  .set { flex-wrap: wrap; }
}
</style>
