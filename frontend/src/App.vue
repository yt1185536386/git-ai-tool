<script setup>
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api, runtimeReady } from './api'
import { appState, aiReady, loadAppState, loadModelOptions } from './store/app'
import { ipcErrorMessage } from './utils/ipcError'

const route = useRoute()
const router = useRouter()

const navItems = [
  { path: '/main', title: '提交与报告', icon: 'Document' },
  { path: '/config', title: '系统配置', icon: 'Setting' }
]

// 启动时加载元信息与配置：配置完整进入主界面，否则引导至配置页
onMounted(async () => {
  if (!runtimeReady()) {
    ElMessage.error('未检测到应用环境，请通过 wails dev 启动')
    return
  }
  await loadAppState()
  // 启动即把当前 Base URL 下的模型列表拉回来，报告面板与编辑弹窗的下拉才能选到多个模型。
  // 不 await：网关慢或不可用时不该拖住首屏，失败静默（下拉会回落到服务商预设）。
  loadModelOptions()
  try {
    const ok = await api.checkConfig()
    // 配置不完整才引导到配置页；完整时不做跳转，
    // 这样直接深链 #/config 也不会被弹回主界面
    if (!ok) router.replace('/config')
  } catch (e) {
    console.error('CheckConfig 调用失败:', e)
    ElMessage.error(ipcErrorMessage(e, '读取配置失败'))
    router.replace('/config')
  }
})
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <div class="brand">
        <div class="brand-logo">
          <el-icon><Cpu /></el-icon>
        </div>
        <div class="brand-text">
          <div class="brand-title">Git AI Tool</div>
          <div class="brand-sub">本地提交扫描 · 大模型报告生成</div>
        </div>
      </div>

      <nav class="app-nav">
        <button
          v-for="item in navItems"
          :key="item.path"
          type="button"
          class="nav-item"
          :class="{ active: route.path === item.path }"
          @click="router.push(item.path)"
        >
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.title }}</span>
        </button>
      </nav>

      <div class="header-right">
        <span class="status-pill" :class="aiReady ? 'ok' : 'warn'">
          <i class="dot"></i>
          {{ aiReady ? '模型已配置' : '未配置模型' }}
        </span>
        <span v-if="appState.meta.versions" class="version">
          v{{ appState.meta.versions.app }}
        </span>
      </div>
    </header>

    <main class="app-main">
      <router-view v-slot="{ Component }">
        <keep-alive>
          <component :is="Component" />
        </keep-alive>
      </router-view>
    </main>
  </div>
</template>

<style scoped>
.app-shell {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.app-header {
  display: flex;
  flex: none;
  gap: 24px;
  align-items: center;
  height: 58px;
  padding: 0 20px;
  background: #fff;
  border-bottom: 1px solid #e8ebf0;
}

.brand {
  display: flex;
  gap: 10px;
  align-items: center;
}

.brand-logo {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  font-size: 18px;
  color: #fff;
  background: linear-gradient(135deg, #3b82f6, #2563eb);
  border-radius: 9px;
}

.brand-title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.2;
  color: #1f2937;
}

.brand-sub {
  font-size: 11px;
  color: #9aa3b2;
}

.app-nav {
  display: flex;
  gap: 4px;
  align-items: center;
  padding-left: 4px;
}

.nav-item {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 7px 14px;
  font-family: inherit;
  font-size: 13px;
  color: #5b6472;
  cursor: pointer;
  background: transparent;
  border: none;
  border-radius: 8px;
  transition: background 0.15s, color 0.15s;
}

.nav-item:hover {
  color: #2563eb;
  background: #f2f6ff;
}

.nav-item.active {
  font-weight: 600;
  color: #2563eb;
  background: #eef4ff;
}

.header-right {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-left: auto;
}

.status-pill {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  padding: 4px 10px;
  font-size: 12px;
  border-radius: 999px;
}

.status-pill .dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.status-pill.ok {
  color: #15803d;
  background: #ecfdf3;
}

.status-pill.ok .dot {
  background: #22c55e;
}

.status-pill.warn {
  color: #b45309;
  background: #fff7ed;
}

.status-pill.warn .dot {
  background: #f59e0b;
}

.version {
  font-size: 12px;
  color: #c0c4cc;
}

.app-main {
  flex: 1;
  min-height: 0;
  padding: 16px;
  overflow: auto;
  background: #f5f7fa;
}
</style>
