<script setup lang="ts">
import { computed } from 'vue'
import { agent } from '../agents'
import { toolName } from '../api'

const props = defineProps<{ id: string; size?: number; label?: boolean }>()
const a = computed(() => agent(props.id))
</script>

<template>
  <span class="agent" :title="toolName(id)">
    <span class="mark" :style="{ '--c': a.color, width: (size ?? 22) + 'px', height: (size ?? 22) + 'px', fontSize: (size ?? 22) * 0.45 + 'px' }">{{ a.mark }}</span>
    <span v-if="label" class="name">{{ toolName(id) }}</span>
  </span>
</template>

<style scoped>
.agent { display: inline-flex; align-items: center; gap: 7px; min-width: 0; }
.mark {
  flex: none; display: grid; place-items: center; border-radius: 7px;
  background: color-mix(in srgb, var(--c) 18%, var(--surface)); color: var(--c);
  border: 1px solid color-mix(in srgb, var(--c) 45%, transparent);
  font: 700 10px/1 var(--font-mono); letter-spacing: -0.02em;
}
.name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
