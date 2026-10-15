<script setup lang="ts">
import { computed, ref } from 'vue'
import { resetAssignments, saveAssignments, store } from '../api'
import type { RoleId } from '../types'

const saved = ref(false)

const has = (toolId: string, role: RoleId) =>
  store.assignments.find((a) => a.toolId === toolId)?.roles.includes(role) ?? false

function toggle(toolId: string, role: RoleId) {
  const a = store.assignments.find((x) => x.toolId === toolId)
  if (!a) return
  if (role === 'lead') {
    // 架构与项目管理只能有一个
    for (const x of store.assignments) x.roles = x.roles.filter((r) => r !== 'lead')
    a.roles.unshift('lead')
  } else if (a.roles.includes(role)) {
    a.roles = a.roles.filter((r) => r !== role)
  } else {
    a.roles.push(role)
  }
  saved.value = false
}

// 每个角色至少要有一个工具承担
const uncovered = computed(() =>
  store.roles.filter((r) => !store.assignments.some((a) => a.roles.includes(r.id))),
)

const lead = computed(() => store.assignments.find((a) => a.roles.includes('lead')))

function save() {
  saveAssignments()
  saved.value = true
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>角色分配</h1>
        <p>只有一个工具时它承担全部角色；多个工具时由能力最强的担任架构与项目管理，其余按擅长方向分配开发、前端、测试、评审。</p>
      </div>
      <div class="row">
        <button class="btn" @click="resetAssignments()">恢复推荐</button>
        <button class="btn primary" :disabled="uncovered.length > 0" @click="save">{{ saved ? '已保存' : '保存分配' }}</button>
      </div>
    </div>

    <p v-if="store.assignments.length === 0 && !store.loading" class="card muted">
      还没有可用的工具，请先到 <RouterLink to="/tools">启动检查</RouterLink> 安装或添加工具。
    </p>

    <template v-else>
      <div class="lead card" v-if="lead">
        <div class="lead-mark" aria-hidden="true">★</div>
        <div>
          <div class="faint">架构与项目管理</div>
          <strong>{{ lead.toolName }}</strong>
          <div class="muted small">负责架构设计、把需求拆成功能/视觉任务、派发给其他工具、汇总进度并集成各分支。</div>
        </div>
      </div>

      <div v-if="uncovered.length" class="notice" role="alert">
        以下角色没有工具承担：{{ uncovered.map((r) => r.name).join('、') }}
      </div>

      <div class="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>工具</th>
              <th v-for="r in store.roles" :key="r.id" :title="r.description">{{ r.name }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in store.assignments" :key="a.toolId">
              <td>
                <strong>{{ a.toolName }}</strong>
                <div v-if="a.manual" class="badge warning">手动执行</div>
              </td>
              <td v-for="r in store.roles" :key="r.id" class="cell">
                <label class="check">
                  <input
                    :type="r.id === 'lead' ? 'radio' : 'checkbox'"
                    name="lead"
                    :checked="has(a.toolId, r.id)"
                    :disabled="r.id === 'lead' && a.manual"
                    @change="toggle(a.toolId, r.id)"
                  />
                  <span class="sr-only">{{ a.toolName }} 担任 {{ r.name }}</span>
                </label>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <h2 class="sec">角色说明</h2>
      <div class="grid">
        <div v-for="r in store.roles" :key="r.id" class="card">
          <strong>{{ r.name }}</strong>
          <p class="muted small">{{ r.description }}</p>
          <div class="row">
            <span v-for="a in store.assignments.filter((x) => x.roles.includes(r.id))" :key="a.toolId" class="badge primary">{{ a.toolName }}</span>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.lead { display: flex; gap: 14px; align-items: center; margin-bottom: 16px; }
.lead-mark { width: 40px; height: 40px; border-radius: 50%; display: grid; place-items: center; background: var(--primary-soft); color: var(--primary); font-size: 18px; }
.small { font-size: 13px; margin: 4px 0 0; }
.cell { text-align: center; }
th:not(:first-child) { text-align: center; }
.check { display: inline-grid; place-items: center; width: 32px; height: 32px; cursor: pointer; }
.check input { width: 16px; height: 16px; accent-color: var(--primary); cursor: pointer; }
.sec { margin: 24px 0 10px; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
td .badge { margin-top: 4px; }
</style>
