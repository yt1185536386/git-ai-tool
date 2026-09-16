import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import {
  ArrowRight,
  Calendar,
  Check,
  Close,
  Connection,
  CopyDocument,
  Cpu,
  Delete,
  Document,
  Download,
  Edit,
  FolderOpened,
  MagicStick,
  Plus,
  Refresh,
  Search,
  Setting,
  VideoPause
} from '@element-plus/icons-vue'
import 'element-plus/dist/index.css'

import App from './App.vue'
import router from './router'
import './styles/global.css'

async function bootstrap() {
  // 在浏览器里直接打开产物预览界面时，注入一份 mock 绑定；
  // 运行在 Wails 容器内（存在 window.runtime）不会加载这一段
  if (!window.runtime) {
    await import('./dev/mockBindings').catch(() => {})
  }

  const app = createApp(App)

  // 只注册实际用到的图标。不要用 `import * as icons` 遍历注册：
  // 那样会把整套图标（300+ 个组件）打进产物并在启动时逐个注册。
  const icons = {
    ArrowRight,
    Calendar,
    Check,
    Close,
    Connection,
    CopyDocument,
    Cpu,
    Delete,
    Document,
    Download,
    Edit,
    FolderOpened,
    MagicStick,
    Plus,
    Refresh,
    Search,
    Setting,
    VideoPause
  }
  for (const [name, component] of Object.entries(icons)) {
    app.component(name, component)
  }

  app.use(ElementPlus, { locale: zhCn })
  app.use(router)
  app.mount('#app')
}

bootstrap()
