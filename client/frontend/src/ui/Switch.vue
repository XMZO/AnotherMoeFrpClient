<script setup lang="ts">
// 开关（受控）：显示 modelValue，点击只发出事件，由父组件决定是否真的改（保存失败时不会跳过去）。
// 用 v-model 时立即生效；只监听 @change 时等父组件更新数据。
const props = defineProps<{ modelValue?: boolean; disabled?: boolean; loading?: boolean; small?: boolean; label?: string }>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; change: [boolean] }>()

function flip() {
  if (props.disabled || props.loading) return
  emit('update:modelValue', !props.modelValue)
  emit('change', !props.modelValue)
}
</script>

<template>
  <button
    type="button"
    role="switch"
    class="sw"
    :class="{ on: modelValue, small, loading }"
    :aria-checked="!!modelValue"
    :aria-label="label"
    :disabled="disabled || loading"
    @click="flip"
  >
    <span class="knob"><span v-if="loading" class="knob-spin" /></span>
  </button>
</template>

<style>
.sw {
  position: relative;
  width: 42px;
  height: 25px;
  flex-shrink: 0;
  border-radius: 99px;
  background: color-mix(in oklab, var(--text-3) 35%, transparent);
  transition: background-color 0.25s;
}
.sw .knob {
  position: absolute;
  top: 3px;
  left: 3px;
  display: grid;
  place-items: center;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: var(--knob);
  box-shadow: 0 1px 3px rgba(20, 10, 40, 0.3);
  transition: transform 0.4s var(--ease-spring);
}
.sw.on {
  background: var(--pri);
}
.sw.on .knob {
  transform: translateX(17px);
}
.sw:disabled:not(.loading) {
  opacity: 0.5;
}
.sw.small {
  width: 34px;
  height: 20px;
}
.sw.small .knob {
  width: 14px;
  height: 14px;
}
.sw.small.on .knob {
  transform: translateX(14px);
}
.knob-spin {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 2px solid var(--pri);
  border-right-color: transparent;
  animation: spin 0.8s linear infinite;
}
</style>
