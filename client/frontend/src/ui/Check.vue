<script setup lang="ts">
import Icon from './Icon.vue'

// 复选框 / 单选卡片（受控）。状态写在 aria-checked 上（e2e 读它）。
const props = defineProps<{ modelValue?: boolean; radio?: boolean; plain?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [boolean] }>()

function flip() {
  if (props.disabled) return
  emit('update:modelValue', props.radio ? true : !props.modelValue)
}
</script>

<template>
  <button
    type="button"
    :role="radio ? 'radio' : 'checkbox'"
    class="check"
    :class="{ on: modelValue, plain, radio }"
    :aria-checked="!!modelValue"
    :disabled="disabled"
    @click="flip"
  >
    <span class="cbx"><Icon name="check" size="xs" /></span>
    <slot />
  </button>
</template>

<style>
.check {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 12px;
  border: 1.5px solid var(--line-2);
  font-weight: 700;
  text-align: left;
  min-width: 0;
  transition: border-color 0.2s, background-color 0.2s;
}
.check:hover:not(:disabled) {
  border-color: color-mix(in oklab, var(--pri) 40%, var(--line-2));
}
.check .cbx {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  border-radius: 7px;
  border: 1.5px solid var(--line);
  color: transparent;
  transition: background-color 0.2s, border-color 0.2s, color 0.2s;
}
.check.radio .cbx {
  border-radius: 50%;
}
.check.on {
  border-color: color-mix(in oklab, var(--pri) 40%, var(--line-2));
  background: var(--pri-soft);
}
.check.on .cbx {
  background: var(--pri);
  border-color: var(--pri);
  color: #fff;
}
.check.on .cbx .ic {
  animation: boing 0.4s var(--ease-spring);
}
.check.plain {
  border: 0;
  background: transparent;
  padding: 2px 0;
}
.check:disabled {
  opacity: 0.55;
}
</style>
