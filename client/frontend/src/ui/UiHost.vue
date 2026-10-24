<script setup lang="ts">
import Icon from './Icon.vue'
import Modal from './Modal.vue'
import { cancelDialog, closeNotice, confirmDialog, dialogs, notices, toasts, type DialogTone, type ToastKind } from './feedback'
import type { IconName } from './icons'

const toastIcon: Record<ToastKind, IconName> = { success: 'check', info: 'info', warning: 'alert', error: 'alert' }
const dialogIcon: Record<DialogTone, IconName> = { success: 'check', info: 'info', warning: 'alert', error: 'alert' }
const noticeTone: Record<string, string> = { info: 'info', warning: 'warn', success: 'ok', error: 'bad' }
</script>

<template>
  <Teleport to="body">
    <TransitionGroup tag="div" name="toast" class="toasts" aria-live="polite">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind" role="status">
        <span class="ti"><Icon :name="toastIcon[t.kind]" size="s" /></span>
        <span class="toast-text">{{ t.text }}</span>
      </div>
    </TransitionGroup>
    <TransitionGroup tag="div" name="notice" class="notices">
      <div v-for="n in notices" :key="n.id" class="notice" role="status">
        <div class="notice-h">
          <span class="tile-ic" :class="noticeTone[n.type ?? 'info']"><Icon :name="n.type === 'warning' ? 'alert' : 'sparkle'" size="s" :fill="n.type !== 'warning'" /></span>
          <b>{{ n.title }}</b>
          <button type="button" class="icon-btn sm notice-close" aria-label="关闭" @click="closeNotice(n.id)"><Icon name="x" size="s" /></button>
        </div>
        <div v-if="n.content" class="notice-body">{{ n.content }}</div>
        <div v-if="n.actions?.length" class="notice-actions">
          <button
            v-for="a in n.actions"
            :key="a.label"
            type="button"
            class="btn xs"
            :class="a.primary ? 'pri' : 'quiet'"
            :data-testid="a.testid"
            @click="a.onClick()"
          >
            {{ a.label }}
          </button>
        </div>
      </div>
    </TransitionGroup>
  </Teleport>
  <Modal v-for="d in dialogs" :key="d.id" :show="true" center :width="400" :title="d.title" :closable="!d.loading" @update:show="cancelDialog(d.id)">
    <div class="big-ic" :class="d.danger ? 'error' : d.tone"><Icon :name="d.icon ?? dialogIcon[d.tone]" /></div>
    <h2 class="dlg-title">{{ d.title }}</h2>
    <p class="dlg-text">{{ d.content }}</p>
    <template #footer>
      <button v-if="d.negativeText" type="button" class="btn quiet sm" :disabled="d.loading" @click="cancelDialog(d.id)">{{ d.negativeText }}</button>
      <button type="button" class="btn sm" :class="d.danger ? 'danger-solid' : 'pri'" :disabled="d.loading" @click="confirmDialog(d.id)">
        <Icon v-if="d.loading" name="refresh" size="s" class="spin" />{{ d.positiveText ?? '确定' }}
      </button>
    </template>
  </Modal>
</template>
