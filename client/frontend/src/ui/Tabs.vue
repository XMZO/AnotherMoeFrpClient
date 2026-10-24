<script setup lang="ts" generic="T extends string">
import { computed } from 'vue'

// 分段式标签页，带滑动的指示块。按钮有 .tab 类（e2e 按文字点它）。
export interface TabOption<V extends string = string> {
  value: V
  label: string
  count?: number
  testid?: string
}
const value = defineModel<T>({ required: true })
const props = defineProps<{ options: TabOption<T>[]; full?: boolean }>()

const index = computed(() => Math.max(0, props.options.findIndex((o) => o.value === value.value)))
const indicator = computed(() => ({
  width: `calc((100% - 8px) / ${Math.max(1, props.options.length)})`,
  transform: `translateX(${index.value * 100}%)`,
}))
</script>

<template>
  <div class="seg" :class="{ full }" role="tablist">
    <span class="seg-ind" :style="indicator" />
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="tab"
      class="tab seg-btn"
      :class="{ on: o.value === value }"
      :aria-selected="o.value === value"
      :data-testid="o.testid"
      @click="value = o.value"
    >
      {{ o.label }}<span v-if="o.count !== undefined" class="seg-n">{{ o.count }}</span>
    </button>
  </div>
</template>

<style>
.seg {
  position: relative;
  display: inline-grid;
  grid-auto-flow: column;
  grid-auto-columns: 1fr;
  padding: 4px;
  border-radius: 15px;
  background: var(--fill);
  transition: background-color 0.35s;
}
.seg.full {
  display: grid;
  width: 100%;
}
.seg-ind {
  position: absolute;
  top: 4px;
  bottom: 4px;
  left: 4px;
  border-radius: 11px;
  background: var(--surface);
  box-shadow: var(--shadow-sm);
  transition: transform 0.45s cubic-bezier(0.3, 1.35, 0.5, 1), width 0.3s;
}
.seg-btn {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 7px 16px;
  border-radius: 11px;
  font-size: 13px;
  font-weight: 800;
  color: var(--text-2);
  white-space: nowrap;
  transition: color 0.2s;
}
.seg-btn:hover {
  color: var(--text);
}
.seg-btn.on {
  color: var(--pri-text);
}
.seg-n {
  min-width: 20px;
  padding: 0 6px;
  border-radius: 99px;
  background: color-mix(in oklab, var(--text-3) 18%, transparent);
  font-size: 11px;
  text-align: center;
}
.seg-btn.on .seg-n {
  background: var(--pri-soft-2);
}
</style>
