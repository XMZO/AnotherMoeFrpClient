<script setup lang="ts">
import { ref } from 'vue'
import Icon from './Icon.vue'

// 输入多个值（域名、允许的用户）：回车或逗号添加，退格删掉最后一个。
const tags = defineModel<string[]>({ default: () => [] })
defineProps<{ placeholder?: string; label?: string; invalid?: boolean }>()
const draft = ref('')

function add() {
  const parts = draft.value
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  if (!parts.length) return
  const next = [...tags.value]
  for (const p of parts) if (!next.includes(p)) next.push(p)
  tags.value = next
  draft.value = ''
}
function remove(i: number) {
  const next = [...tags.value]
  next.splice(i, 1)
  tags.value = next
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',' || e.key === '，') {
    e.preventDefault()
    add()
  } else if (e.key === 'Backspace' && !draft.value && tags.value.length) {
    remove(tags.value.length - 1)
  }
}
</script>

<template>
  <div class="tags" :class="{ err: invalid }">
    <TransitionGroup name="fade">
      <span v-for="(t, i) in tags" :key="t" class="tag">
        {{ t }}<button type="button" :aria-label="`移除 ${t}`" @click="remove(i)"><Icon name="x" size="xs" /></button>
      </span>
    </TransitionGroup>
    <input v-model="draft" :placeholder="tags.length ? '' : placeholder" :aria-label="label" @keydown="onKey" @blur="add" />
  </div>
</template>

<style>
.tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-height: 40px;
  padding: 5px 6px;
  border-radius: 13px;
  border: 1.5px solid var(--line);
  background: var(--surface-2);
  transition: border-color 0.2s, box-shadow 0.2s;
}
.tags:focus-within {
  border-color: var(--pri);
  box-shadow: 0 0 0 4px var(--pri-soft);
}
.tags.err {
  border-color: var(--bad);
}
.tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 3px 4px 3px 9px;
  border-radius: 9px;
  background: var(--pri-soft);
  color: var(--pri-text);
  font-size: 12px;
  font-weight: 800;
}
.tag button {
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  border-radius: 6px;
}
.tag button:hover {
  background: var(--pri-soft-2);
}
.tags input {
  flex: 1;
  min-width: 90px;
  height: 28px;
  border: 0;
  background: transparent;
  outline: none;
  padding: 0 6px;
}
</style>
