import { createRouter, createWebHashHistory } from 'vue-router'
import MainView from '../views/MainView.vue'
import ConfigView from '../views/ConfigView.vue'

// Wails WebView 从本地加载资源，必须用 hash 模式
const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/main' },
    { path: '/main', name: 'main', component: MainView, meta: { title: '提交记录与报告' } },
    { path: '/config', name: 'config', component: ConfigView, meta: { title: '系统配置' } }
  ]
})

export default router
