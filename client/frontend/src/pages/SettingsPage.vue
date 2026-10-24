<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '../ui/Icon.vue'
import Switch from '../ui/Switch.vue'
import Options from '../ui/Options.vue'
import { message } from '../ui/feedback'
import { useSettingsStore } from '../stores/settings'
import { describeError } from '../errors'
import type { Settings } from '../api'

const settings = useSettingsStore()
const s = computed(() => settings.settings)
const levels = ['trace', 'debug', 'info', 'warn', 'error'].map((l) => ({ value: l, label: l }))
const themes = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
]

// 自定义代理：先填地址，点"应用"才切过去（地址不对时服务端请求会全部失败，所以单独确认）
const proxyUrl = ref('')
watch(
  () => settings.settings?.proxyUrl,
  (v) => (proxyUrl.value = v ?? ''),
  { immediate: true },
)
const customSelected = ref(false)
watch(
  () => settings.settings?.networkProxy,
  (v) => (customSelected.value = v === 'custom'),
  { immediate: true },
)
const netMode = computed(() => (customSelected.value ? 'custom' : (s.value?.networkProxy ?? 'system')))

async function set(patch: Partial<Settings>) {
  try {
    await settings.update(patch)
    return true
  } catch (e) {
    message.error(describeError(e))
    return false
  }
}

async function onProxyMode(v: string) {
  if (v === 'custom') {
    customSelected.value = true // 等填好地址再应用
    return
  }
  customSelected.value = false
  await set({ networkProxy: v })
}

async function applyCustom() {
  if (await set({ networkProxy: 'custom', proxyUrl: proxyUrl.value.trim() })) message.success('已切换到自定义代理')
}
</script>

<template>
  <div class="page dotted-bg settings-page">
    <div class="inner">
      <div class="pg-head">
        <div>
          <h1>设置</h1>
          <p>改动会立即生效并自动保存。</p>
        </div>
      </div>
      <div v-if="s" class="set-grid">
        <section class="card">
          <div class="card-h"><span class="tile-ic"><Icon name="sun" size="s" /></span>外观</div>
          <div class="set-cols">
          <div class="set-row">
          <div class="set-txt"><b>主题</b><small>“跟随系统”随 Windows 切换</small></div>
          <div class="tiles" role="radiogroup" aria-label="主题" data-testid="settings-theme">
            <button
              v-for="t in themes"
              :key="t.value"
              type="button"
              role="radio"
              class="tile radio"
              :class="{ on: s.theme === t.value }"
              :aria-checked="s.theme === t.value"
              @click="set({ theme: t.value })"
            >
              <span class="mini" :class="t.value">
                <span class="m-half l"><span class="mr" /><span class="mc"><i class="p" /><i /><i /></span></span>
                <span class="m-half d"><span class="mr" /><span class="mc"><i class="p" /><i /><i /></span></span>
              </span>
              {{ t.label }}
            </button>
          </div>
          </div>
          <div class="set-row">
            <div class="set-txt"><b>图片面板</b><small>侧栏里每分钟换一张图；关闭后不会发出任何图片请求</small></div>
            <Switch :model-value="s.imagePanel" label="图片面板" data-testid="settings-image-panel" @change="(v: boolean) => set({ imagePanel: v })" />
          </div>
          </div>
        </section>

        <section class="card" style="animation-delay: 0.05s">
          <div class="card-h"><span class="tile-ic"><Icon name="power" size="s" /></span>窗口与启动</div>
          <div class="set-cols">
          <div class="set-row">
            <div class="set-txt"><b>点关闭按钮时</b></div>
            <Options
              :model-value="s.closeAction"
              :options="[
                { value: 'tray', label: '最小化到托盘' },
                { value: 'quit', label: '退出程序' },
              ]"
              label="点关闭按钮时"
              @pick="(v: string) => set({ closeAction: v })"
            />
          </div>
          <div class="set-row" :class="{ dim: s.closeAction !== 'tray' }">
            <div class="set-txt"><b>最小化到托盘时释放界面内存</b><small>后台只占几十 MB（隧道照常运行），再次打开窗口约需半秒</small></div>
            <Switch :model-value="s.releaseOnHide" :disabled="s.closeAction !== 'tray'" label="释放界面内存" data-testid="settings-release-on-hide" @change="(v: boolean) => set({ releaseOnHide: v })" />
          </div>
          <div class="set-row">
            <div class="set-txt"><b>开机自启</b><small>开机后只在托盘显示图标，不弹出窗口</small></div>
            <Switch :model-value="s.autoStart" label="开机自启" data-testid="settings-autostart" @change="(v: boolean) => set({ autoStart: v })" />
          </div>
          <div class="set-row">
            <div class="set-txt"><b>启动后自动连接</b><small>启动时自动运行上次使用的配置和节点</small></div>
            <Switch :model-value="s.autoConnect" label="启动后自动连接" data-testid="settings-autoconnect" @change="(v: boolean) => set({ autoConnect: v })" />
          </div>
          </div>
        </section>

        <section class="card" style="animation-delay: 0.1s">
          <div class="card-h"><span class="tile-ic"><Icon name="globe" size="s" /></span>网络与日志</div>
          <div class="set-cols three">
            <div class="set-row col">
              <div class="set-txt"><b>应用的网络代理</b><small>登录、同步、检查更新、图片面板走它。“跟随系统”读 Internet 选项里的手动代理</small></div>
              <div class="net-row">
                <Options
                  :model-value="netMode"
                  :options="[
                    { value: 'system', label: '跟随系统' },
                    { value: 'none', label: '不使用' },
                    { value: 'custom', label: '自定义', testid: 'settings-proxy-custom' },
                  ]"
                  label="网络代理"
                  data-testid="settings-network-proxy"
                  @pick="onProxyMode"
                />
                <Transition name="collapse">
                  <form v-if="customSelected" class="inp-group" @submit.prevent="applyCustom">
                    <label for="proxy-url" class="sr-only">代理地址</label>
                    <input id="proxy-url" v-model="proxyUrl" class="inp mono" placeholder="http://127.0.0.1:7890 或 socks5://…" data-testid="settings-proxy-url" />
                    <button type="submit" class="btn pri sm" style="height: 40px" data-testid="settings-proxy-apply">应用</button>
                  </form>
                </Transition>
              </div>
            </div>
            <div class="set-row col">
              <div class="set-txt"><b>frp 隧道连接</b><small>连节点时走不走上面的代理（节点自己填了代理的用节点的），重新启动隧道生效</small></div>
              <Options
                :model-value="s.frpProxy === 'app' ? 'app' : 'direct'"
                :options="[
                  { value: 'direct', label: '直连' },
                  { value: 'app', label: '跟随网络代理', testid: 'settings-frp-proxy-app' },
                ]"
                label="frp 隧道连接"
                data-testid="settings-frp-proxy"
                @pick="(v: string) => set({ frpProxy: v })"
              />
            </div>
            <div class="set-row col">
              <div class="set-txt"><b>frp 日志级别</b><small>排查问题时调到 debug，平时 info 就够了</small></div>
              <select class="inp mono log-sel" :value="s.logLevel" aria-label="frp 日志级别" data-testid="settings-log-level" @change="(e: Event) => set({ logLevel: (e.target as HTMLSelectElement).value })">
                <option v-for="l in levels" :key="l.value" :value="l.value">{{ l.label }}</option>
              </select>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style>
/* 卡片整行堆叠，卡片里按两列排：左右边缘天然对齐，每张卡片的高度只由自己的内容决定，不拉伸、不留白 */
.set-grid {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.set-grid > .card {
  container-type: inline-size;
}
.set-cols {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 28px;
  align-items: start;
}
.set-row {
  display: flex;
  align-items: center;
  gap: 6px 14px;
  min-height: 52px;
  padding: 9px 0;
  border-top: 1.5px dashed var(--line-2);
}
/* 卡片标题下面的第一项、两列各自的第一项不要分隔线 */
.card-h + .set-row {
  border-top: 0;
}
.set-cols > .set-row:nth-child(-n + 2) {
  border-top: 0;
}
.set-row > .set-txt {
  flex: 1;
  min-width: 0;
}
.set-row > :not(.set-txt) {
  flex-shrink: 0;
}
.set-row.col {
  flex-direction: column;
  align-items: flex-start;
}
.set-row.col > .set-txt {
  flex: none;
}
.set-row.dim .set-txt {
  opacity: 0.55;
}
/* 设置页一屏放下：标题区和卡片里的留白收紧 */
.settings-page {
  padding-top: 16px;
  padding-bottom: 16px;
}
.settings-page .pg-head {
  margin-bottom: 12px;
}
.set-grid > .card .card-h {
  padding: 10px 0 2px;
}
.log-sel {
  width: 140px;
  height: 36px;
}
/* 网络与日志：三列 */
.set-cols.three {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.set-cols.three > .set-row:nth-child(3) {
  border-top: 0;
}
.set-note {
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
  line-height: 1.6;
}
.set-note b {
  display: block;
  margin-bottom: 2px;
  font-size: 14px;
  font-weight: 800;
  color: var(--text);
}
.net-row {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
  width: 100%;
}
.net-row .inp-group {
  width: 100%;
}
/* 窄了退成一列 */
@container (max-width: 700px) {
  .set-cols.three {
    grid-template-columns: minmax(0, 1fr);
  }
  .set-cols.three > .set-row:nth-child(n + 2) {
    border-top: 1.5px dashed var(--line-2);
  }
}
@container (max-width: 640px) {
  .set-cols {
    grid-template-columns: minmax(0, 1fr);
  }
  .set-cols > .set-row:nth-child(2) {
    border-top: 1.5px dashed var(--line-2);
  }
}
.tiles {
  display: grid;
  flex-shrink: 0;
  grid-template-columns: repeat(3, 78px);
  gap: 8px;
}
.tile {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 4px 4px 5px;
  border-radius: 12px;
  border: 2px solid var(--line-2);
  font-size: 11.5px;
  font-weight: 800;
  color: var(--text-2);
  transition: border-color 0.2s, box-shadow 0.2s, transform 0.2s var(--ease-spring);
}
.tile:hover {
  transform: translateY(-2px);
}
.tile.on {
  border-color: var(--pri);
  color: var(--pri-text);
  box-shadow: 0 0 0 4px var(--pri-soft);
}
.mini {
  position: relative;
  display: block;
  width: 100%;
  aspect-ratio: 16 / 10;
  border-radius: 7px;
  overflow: hidden;
  border: 1px solid rgba(120, 100, 180, 0.25);
}
.m-half {
  position: absolute;
  inset: 0;
  display: flex;
}
.mini.light .m-half.d,
.mini.dark .m-half.l {
  display: none;
}
.mini.system .m-half.d {
  clip-path: polygon(60% 0, 100% 0, 100% 100%, 40% 100%);
}
.mr {
  width: 22%;
}
.mc {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 5px 4px;
}
.mc i {
  display: block;
  height: 4px;
  border-radius: 3px;
}
.m-half.l .mr {
  background: #eeeaf9;
}
.m-half.l .mc {
  background: #f6f4fc;
}
.m-half.l .mc i {
  background: #fff;
  box-shadow: 0 0 0 1px #e2dcf1;
}
.m-half.d .mr {
  background: #100d18;
}
.m-half.d .mc {
  background: #14111d;
}
.m-half.d .mc i {
  background: #1f1a2d;
  box-shadow: 0 0 0 1px #352e4c;
}
.mc i.p {
  width: 45%;
  background: var(--pri) !important;
  box-shadow: none !important;
}
</style>
