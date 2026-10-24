<script setup lang="ts" generic="T extends string">
// 单选（胶囊样式，受控）。每个选项是 .radio 按钮（e2e 按文字点它）。
// v-model 时立即生效；只监听 @pick 时由父组件保存成功后再更新 modelValue。
export interface OptionItem<V extends string = string> {
  value: V
  label: string
  testid?: string
}
defineProps<{ modelValue: T; options: OptionItem<T>[]; mono?: boolean; label?: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [T]; pick: [T] }>()

function pick(v: T) {
  emit('update:modelValue', v)
  emit('pick', v)
}
</script>

<template>
  <div class="opts" role="radiogroup" :aria-label="label">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      class="radio opt"
      :class="{ on: o.value === modelValue, mono }"
      :aria-checked="o.value === modelValue"
      :data-testid="o.testid"
      :disabled="disabled"
      @click="pick(o.value)"
    >
      {{ o.label }}
    </button>
  </div>
</template>

<style>
.opts {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 2px;
  padding: 3px;
  border-radius: 13px;
  background: var(--fill);
}
.opt {
  padding: 6px 12px;
  border-radius: 10px;
  font-size: 12.5px;
  font-weight: 800;
  color: var(--text-2);
  transition: background-color 0.2s, color 0.2s, box-shadow 0.2s;
}
.opt:hover {
  color: var(--text);
}
.opt.on {
  background: var(--surface);
  color: var(--pri-text);
  box-shadow: var(--shadow-sm);
}
.opt.mono {
  font-family: var(--mono);
  font-size: 11.5px;
  font-weight: 600;
}
</style>
