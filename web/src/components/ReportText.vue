<script setup lang="ts">
import { computed, ref } from 'vue'

// 工具汇报常带 markdown（**粗体**、`代码`、- 列表），而且可能很长：
// 先转义再做少量格式化，超过几行时折叠，点「展开」看全文。
const props = defineProps<{ text?: string; lines?: number }>()
const open = ref(false)

function esc(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

const html = computed(() => {
  let s = esc((props.text ?? '').trim())
  // 挤在一行里的列表项（" - xxx"）拆成新行
  s = s.replace(/\s+-\s+(?=\S)/g, '\n• ').replace(/^-\s+/gm, '• ')
  s = s.replace(/\*\*([^*\n]+)\*\*/g, '<b>$1</b>')
  s = s.replace(/`([^`\n]+)`/g, '<code>$1</code>')
  return s
})
const long = computed(() => (props.text ?? '').length > 160 || (props.text ?? '').split('\n').length > (props.lines ?? 4))
</script>

<template>
  <div class="rt">
    <p class="body" :class="{ clamp: long && !open }" :style="{ '--lines': lines ?? 4 }" v-html="html"></p>
    <button v-if="long" type="button" class="more" @click="open = !open">{{ open ? '收起' : '展开' }}</button>
  </div>
</template>

<style scoped>
.rt { min-width: 0; display: grid; gap: 2px; justify-items: start; }
.body { margin: 0; font-size: 13px; white-space: pre-line; overflow-wrap: anywhere; word-break: break-word; min-width: 0; max-width: 100%; }
.body :deep(code) { font-size: 12px; background: var(--sunken); padding: 0 4px; border-radius: 4px; }
.clamp { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: var(--lines); line-clamp: var(--lines); overflow: hidden; }
.more { font: inherit; font-size: 12px; color: var(--accent); background: none; border: 0; padding: 0; cursor: pointer; }
.more:hover { text-decoration: underline; }
</style>
