import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import App from './App.vue'
import router from './router/index.js'
import { initI18n } from './i18n.js'
import { initTheme } from './theme.js'
import './style.css'

// 主题初始化(避免刷新闪烁;支持 auto 跟随系统)
initTheme()

const app = createApp(App)
app.use(ElementPlus, { locale: zhCn })
app.use(router)
app.mount('#app')
initI18n()
