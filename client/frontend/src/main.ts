import { createApp } from 'vue'
import { createPinia } from 'pinia'
import '@fontsource-variable/nunito'
import '@fontsource-variable/jetbrains-mono'
// 全局样式先引入，组件自己的样式在后面，同等优先级时组件的生效
import './style.css'
import './ui/overlay.css'
import App from './App.vue'

createApp(App).use(createPinia()).mount('#app')
