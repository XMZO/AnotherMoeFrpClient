<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Clipboard } from '@wailsio/runtime'
import Icon from '../ui/Icon.vue'
import { message } from '../ui/feedback'
import { AppService, type License, type StartupInfo, type UpdateInfo } from '../api'
import { describeError, toAppError } from '../errors'
import { formatTime } from '../components/shareFormat'
import mascot from '../assets/mascot.png'

const info = ref<StartupInfo | null>(null)
const update = ref<UpdateInfo | null>(null)
const licenses = ref<License[]>([])
const checking = ref(false)
const checkError = ref('')

onMounted(async () => {
  info.value = await AppService.Startup()
  update.value = await AppService.UpdateInfo()
  licenses.value = (await AppService.Licenses()) ?? []
})

const open = (url: string) => AppService.OpenURL(url).catch(() => {})

async function check() {
  checking.value = true
  checkError.value = ''
  try {
    update.value = await AppService.CheckUpdate()
  } catch (e) {
    checkError.value = describeError(toAppError(e))
  } finally {
    checking.value = false
  }
}

async function copy(text: string) {
  try {
    await Clipboard.SetText(text)
    message.success('已复制')
  } catch {
    message.info(text)
  }
}
</script>

<template>
  <div class="page dotted-bg about-page">
    <div class="inner">
      <section class="about-hero">
        <Icon name="sparkle" fill class="deco a" />
        <Icon name="sparkle" fill class="deco b" />
        <Icon name="sparkle" fill class="deco c" />
        <img :src="mascot" alt="MoeFrp 图标" />
        <div class="hero-txt">
          <h1>MoeFrp</h1>
          <p>萌！FRP 高级客户端 · frp 以库的形式内嵌</p>
          <div v-if="info" class="hero-meta">
            <span class="bdg pri lg">客户端 <span data-testid="about-version">{{ info.version.app }}</span></span>
            <span class="bdg lg">frp {{ info.version.frp }}</span>
            <span class="bdg lg">Wails {{ info.version.wails }}</span>
            <span class="bdg lg">{{ info.version.go }}</span>
          </div>
        </div>
        <div class="hero-act">
          <button type="button" class="btn pri" :disabled="checking" data-testid="about-check-update" @click="check">
            <Icon name="refresh" size="s" :class="{ spin: checking }" />{{ checking ? '检查中…' : '检查更新' }}
          </button>
          <small v-if="update?.checkedAt">上次检查：{{ formatTime(update.checkedAt) }}</small>
        </div>
      </section>

      <Transition name="collapse">
        <section v-if="update?.available" class="update-card" data-testid="about-update-available">
          <div class="uc-head">
            <span class="tile-ic lg gold"><Icon name="sparkle" fill /></span>
            <div class="uc-t">
              <b>有新版本 {{ update.latest }}</b>
              <small>当前 {{ update.current }}{{ update.required ? ' · 已低于最低要求，请尽快更新' : '' }}</small>
            </div>
            <button v-if="update.downloadUrl" type="button" class="btn pri sm" @click="open(update.downloadUrl)"><Icon name="download" size="s" />下载</button>
          </div>
          <div v-if="update.releaseNotes" class="uc-notes">{{ update.releaseNotes }}</div>
          <div v-if="update.sha256" class="uc-sha mono">SHA256：{{ update.sha256 }}</div>
        </section>
      </Transition>
      <div v-if="!update?.available && update?.checkedAt && !update.error" class="alert ok about-note" data-testid="about-up-to-date"><Icon name="check" size="s" /><span>已是最新版本</span></div>
      <div v-if="checkError" class="alert bad about-note"><Icon name="alert" size="s" /><span>{{ checkError }}</span></div>

      <div class="about-grid">
          <section v-if="update?.announcement" class="card" data-testid="about-announcement">
            <div class="card-h"><span class="tile-ic gold"><Icon name="bell" size="s" /></span>公告</div>
            <p class="ann-text">{{ update.announcement }}</p>
          </section>
          <section v-if="info" class="card" style="animation-delay: 0.05s">
            <div class="card-h"><span class="tile-ic"><Icon name="folder" size="s" /></span>数据位置</div>
            <div class="about-cols">
            <div class="kv-row">
              <small>设置和配置缓存</small>
              <span class="mono" :title="info.dataDir">{{ info.dataDirShort || info.dataDir }}</span>
              <button type="button" class="icon-btn sm" title="复制路径" aria-label="复制设置和配置缓存的路径" @click="copy(info.dataDir)"><Icon name="copy" size="s" /></button>
            </div>
            <div class="kv-row">
              <small>WebView2 数据和日志</small>
              <span class="mono" :title="info.localDir">{{ info.localDirShort || info.localDir }}</span>
              <button type="button" class="icon-btn sm" title="复制路径" aria-label="复制 WebView2 数据和日志的路径" @click="copy(info.localDir)"><Icon name="copy" size="s" /></button>
            </div>
            </div>
          </section>
        <section class="card" style="animation-delay: 0.1s">
          <div class="card-h"><span class="tile-ic"><Icon name="heart" size="s" /></span>开源许可</div>
          <div class="about-cols" data-testid="about-licenses">
            <div v-for="l in licenses" :key="l.name" class="lic">
              <a :href="l.url" @click.prevent="open(l.url)">{{ l.name }}</a>
              <span class="bdg">{{ l.license }}</span>
              <small>{{ l.note }}</small>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style>
.about-page {
  padding-top: 14px;
  padding-bottom: 14px;
}
.about-hero {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 16px 22px;
  padding: 18px 22px;
  border-radius: 26px;
  background: var(--surface);
  border: 1.5px solid var(--line-2);
  box-shadow: var(--shadow-sm);
  overflow: hidden;
  animation: rowIn 0.5s var(--ease-out) backwards;
}
.about-hero img {
  width: 88px;
  height: 88px;
  border-radius: 28px;
  box-shadow: 0 0 0 4px var(--surface), 0 16px 34px -12px color-mix(in oklab, var(--pri) 70%, transparent);
  animation: floaty 4.5s ease-in-out infinite;
}
.about-hero h1 {
  font-size: 28px;
  font-weight: 900;
  letter-spacing: -0.01em;
}
.about-hero p {
  margin-top: 2px;
  font-size: 13px;
  color: var(--text-2);
  font-weight: 700;
}
.hero-txt {
  flex: 1 1 240px;
  min-width: 0;
}
.hero-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}
.hero-act {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
}
.hero-act small {
  font-size: 12px;
  color: var(--text-3);
  font-weight: 600;
}
.deco {
  position: absolute;
  color: var(--gold);
  pointer-events: none;
}
.deco.a {
  top: 18px;
  right: 30%;
  width: 16px;
  height: 16px;
  animation: twinkle 2.6s ease-in-out infinite;
}
.deco.b {
  bottom: 22px;
  right: 36%;
  width: 10px;
  height: 10px;
  animation: twinkle 3.2s ease-in-out infinite 0.8s;
}
.deco.c {
  top: 30px;
  left: 120px;
  width: 12px;
  height: 12px;
  animation: twinkle 2.9s ease-in-out infinite 0.4s;
}
.update-card {
  margin-top: 14px;
  padding: 16px;
  border-radius: 22px;
  background: var(--gold-soft);
  border: 1.5px solid color-mix(in oklab, var(--gold) 40%, transparent);
}
.uc-head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.uc-t {
  flex: 1;
  min-width: 0;
}
.uc-t b {
  display: block;
  font-size: 15.5px;
  font-weight: 900;
}
.uc-t small {
  display: block;
  font-size: 12px;
  color: var(--gold-text);
  font-weight: 700;
}
.uc-notes {
  margin-top: 10px;
  white-space: pre-wrap;
  font-size: 12.5px;
  color: var(--text-2);
  font-weight: 600;
  line-height: 1.6;
}
.uc-sha {
  margin-top: 8px;
  font-size: 11.5px;
  color: var(--text-3);
  word-break: break-all;
}
.about-note {
  margin-top: 14px;
}
/* 卡片整行堆叠，卡片里按两列排：左右边缘对齐，高度只由自己的内容决定，不拉伸、不留白 */
.about-grid {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 12px;
}
.about-grid > .card {
  container-type: inline-size;
  padding-bottom: 6px;
}
.about-cols {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 36px;
}
/* 两列各自的第一项不要分隔线 */
.about-cols > :nth-child(-n + 2) {
  border-top: 0 !important;
}
@container (max-width: 640px) {
  .about-cols {
    grid-template-columns: minmax(0, 1fr);
  }
  .about-cols > :nth-child(2) {
    border-top: 1.5px dashed var(--line-2) !important;
  }
}
.ann-text {
  padding-bottom: 14px;
  white-space: pre-wrap;
  font-size: 13px;
  color: var(--text-2);
  font-weight: 600;
  line-height: 1.65;
}
.kv-row {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  padding: 10px 0;
  border-top: 1.5px dashed var(--line-2);
}
.kv-row small {
  font-size: 12px;
  font-weight: 800;
  color: var(--text-3);
}
/* 路径用 %APPDATA% 这样的短写法，一般一行放得下；真放不下（自定义了数据目录）就折行显示全，不截断 */
.kv-row span {
  min-width: 0;
  overflow-wrap: anywhere;
}
.lic {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  border-top: 1.5px dashed var(--line-2);
}
.lic a {
  flex-shrink: 0;
  font-weight: 800;
  text-decoration: none;
  white-space: nowrap;
}
.lic .bdg {
  flex-shrink: 0;
}
.lic a:hover {
  text-decoration: underline;
}
.lic small {
  min-width: 0;
  margin-left: auto;
  font-size: 11.5px;
  color: var(--text-3);
  font-weight: 600;
  text-align: right;
}
</style>
