<script setup lang="ts">
import { icons, type IconName } from './icons'

withDefaults(defineProps<{ name: IconName; size?: 'xs' | 's' | 'm' | 'l'; fill?: boolean }>(), { size: 'm', fill: false })
</script>

<template>
  <svg :class="['ic', size, { fill }]" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path :d="icons[name]" /></svg>
</template>

<style>
.ic {
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  flex-shrink: 0;
  display: block;
}
.ic.xs {
  width: 13px;
  height: 13px;
  stroke-width: 2.2;
}
.ic.s {
  width: 16px;
  height: 16px;
}
.ic.l {
  width: 22px;
  height: 22px;
}
.ic.fill {
  fill: currentColor;
  stroke: none;
}
</style>
