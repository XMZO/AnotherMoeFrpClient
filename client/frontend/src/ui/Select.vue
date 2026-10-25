<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import Icon from './Icon.vue'

// 下拉选择，代替原生 <select>：样式和输入框一致，弹出层和菜单一样有动画，选中项带勾。
// 弹出层 Teleport 到 body、fixed 定位，不会被卡片或抽屉裁掉；宽度跟按钮一样。
// 鼠标选完后按钮失焦（不留焦点高亮）；键盘：↑↓ 移动、Enter/空格 选择、Esc/Tab 关闭。
export interface SelectOption {
  value: string
  label: string
  hint?: string // 右边的小字（比如延迟），只在弹出层里显示
  tone?: 'good' | 'mid' | 'bad' | '' // hint 的颜色
}
defineOptions({ inheritAttrs: false })
const model = defineModel<string>({ default: '' })
const props = defineProps<{
  options: SelectOption[]
  id?: string
  mono?: boolean
  disabled?: boolean
  placeholder?: string
}>()
const emit = defineEmits<{ change: [string] }>()

const open = ref(false)
const active = ref(-1)
const btn = ref<HTMLButtonElement | null>(null)
const pop = ref<HTMLElement | null>(null)
const pos = ref<Record<string, string>>({ left: '-9999px', top: '0px' })

const current = computed(() => props.options.find((o) => o.value === model.value))

async function place() {
  await nextTick()
  const b = btn.value
  const p = pop.value
  if (!b || !p) return
  const r = b.getBoundingClientRect()
  const h = p.offsetHeight
  let y = r.bottom + 6
  let origin = 'top center'
  if (y + h > window.innerHeight - 8 && r.top - h - 6 > 8) {
    y = r.top - h - 6
    origin = 'bottom center'
  }
  const x = Math.min(Math.max(8, r.left), window.innerWidth - r.width - 8)
  pos.value = { left: `${x}px`, top: `${y}px`, width: `${r.width}px`, transformOrigin: origin }
  p.querySelector<HTMLElement>('.sel-opt.on')?.scrollIntoView({ block: 'nearest' })
}

function show() {
  if (props.disabled || open.value) return
  active.value = Math.max(0, props.options.findIndex((o) => o.value === model.value))
  open.value = true
}
function hide() {
  open.value = false
}
function pick(i: number, byMouse: boolean) {
  const o = props.options[i]
  if (!o) return
  hide()
  if (o.value !== model.value) {
    model.value = o.value
    emit('change', o.value)
  }
  if (byMouse) btn.value?.blur()
  else btn.value?.focus()
}

function onKey(e: KeyboardEvent) {
  const n = props.options.length
  if (!open.value) {
    if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) {
      e.preventDefault()
      show()
    }
    return
  }
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    active.value = (active.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
    nextTick(() => pop.value?.querySelectorAll<HTMLElement>('.sel-opt')[active.value]?.scrollIntoView({ block: 'nearest' }))
  } else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    pick(active.value, false)
  } else if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation() // 在弹窗、抽屉里时不连带关掉它们
    hide()
  } else if (e.key === 'Tab') hide()
}

function onDocDown(e: Event) {
  const t = e.target as Node
  if (pop.value?.contains(t) || btn.value?.contains(t)) return
  hide()
}
// 页面、卡片、抽屉滚动时关掉（弹出层是 fixed 定位，不跟着滚）；弹出层自己滚动不算
function onScroll(e: Event) {
  if (!pop.value?.contains(e.target as Node)) hide()
}
function listen(on: boolean) {
  if (on) {
    document.addEventListener('mousedown', onDocDown, true)
    document.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', hide)
    window.addEventListener('blur', hide)
  } else {
    document.removeEventListener('mousedown', onDocDown, true)
    document.removeEventListener('scroll', onScroll, true)
    window.removeEventListener('resize', hide)
    window.removeEventListener('blur', hide)
  }
}
watch(open, (v) => {
  listen(v)
  if (v) place()
})
onBeforeUnmount(() => listen(false))
</script>

<template>
  <button
    :id="id"
    ref="btn"
    type="button"
    class="inp sel-btn"
    :class="{ mono, open }"
    :disabled="disabled"
    aria-haspopup="listbox"
    :aria-expanded="open"
    v-bind="$attrs"
    @click="open ? hide() : show()"
    @keydown="onKey"
  >
    <span class="sel-val" :class="{ faint: !current }">{{ current?.label ?? placeholder ?? '' }}</span>
    <Icon name="chev" size="xs" class="sel-arrow" />
  </button>
  <Teleport to="body">
    <Transition name="menu">
      <div v-if="open" ref="pop" class="menu sel-pop" role="listbox" :style="pos">
        <button
          v-for="(o, i) in options"
          :key="o.value"
          type="button"
          role="option"
          class="menu-item sel-opt"
          :class="{ on: o.value === model, act: i === active, mono }"
          :aria-selected="o.value === model"
          tabindex="-1"
          @mouseenter="active = i"
          @click="pick(i, true)"
        >
          <span class="sel-opt-l">{{ o.label }}</span>
          <span v-if="o.hint" class="sel-opt-hint" :class="o.tone">{{ o.hint }}</span>
          <Icon v-if="o.value === model" name="check" size="xs" />
        </button>
      </div>
    </Transition>
  </Teleport>
</template>

<style>
.sel-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  text-align: left;
  cursor: pointer;
  color: var(--text);
}
/* 只在展开或键盘聚焦时显示焦点高亮，鼠标选完不残留 */
.sel-btn:focus:not(:focus-visible):not(.open) {
  border-color: var(--line);
  box-shadow: none;
  background: var(--surface-2);
}
.sel-btn.open {
  border-color: var(--pri);
  box-shadow: 0 0 0 4px var(--pri-soft);
  background: var(--surface);
}
.sel-val {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sel-arrow {
  flex: none;
  color: var(--text-3);
  transition: transform 0.25s var(--ease-pop);
}
.sel-btn.open .sel-arrow {
  transform: rotate(180deg);
}
.sel-pop {
  min-width: 0;
  z-index: 1200; /* 在弹窗（1050–1100）上面：弹窗里的下拉框不被盖住 */
}
.sel-opt {
  justify-content: space-between;
  font-weight: 600;
}
.sel-opt.mono .sel-opt-l {
  font-family: var(--mono);
}
.sel-opt.act {
  background: var(--fill);
}
.sel-opt.on {
  color: var(--pri-text);
  font-weight: 800;
}
.sel-opt .ic {
  color: var(--pri);
}
.sel-opt-l {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sel-opt-hint {
  flex: none;
  font-family: var(--mono);
  font-size: 12px;
  font-weight: 700;
  color: var(--text-3);
}
.sel-opt-hint.good {
  color: var(--ok-text);
}
.sel-opt-hint.mid {
  color: var(--warn-text);
}
.sel-opt-hint.bad {
  color: var(--bad-text);
}
</style>
