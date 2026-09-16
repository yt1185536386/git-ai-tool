<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, runtimeReady } from '../api'
import { appState, applyConfig, loadAppState } from '../store/app'
import { ipcErrorMessage } from '../utils/ipcError'

const meta = computed(() => appState.meta || {})
const providers = ref([])
const saving = ref(false)
const testing = ref(false)
const fetchingModels = ref(false)
const effectiveBaseUrl = ref('')
/** 配置是否成功读取：读失败时禁止保存，避免把用户原有配置覆盖成空 */
const loaded = ref(false)

const config = reactive({
  ai: { provider: 'deepseek', apiKey: '', baseUrl: '', model: '', temperature: 0.3, maxChars: 24000 },
  workspaces: [],
  scan: { sinceDays: 7, maxDepth: 3, maxCommits: 800 }
})
/** 已保存过 API Key：保存后不回显明文，输入框留空表示保持不变 */
const apiKeyConfigured = ref(false)
const newWorkspace = ref('')
/** 工作区目录选择中 */
const picking = ref(false)

const currentModels = computed(() => {
  const found = providers.value.find((p) => p.value === config.ai.provider)
  return found?.models || []
})

const onProviderChange = (value) => {
  const preset = providers.value.find((p) => p.value === value)
  if (!preset) return
  if (preset.baseUrl) config.ai.baseUrl = preset.baseUrl
  if (preset.models?.length) config.ai.model = preset.models[0]
}

// 查询当前 Base URL 下的全部模型，并填入下拉框
const fetchModels = async () => {
  fetchingModels.value = true
  try {
    const res = await api.listModels({ ...config.ai })
    if (res?.ok && res.models?.length) {
      const merged = [...new Set([...res.models, ...currentModels.value])]
      if (config.ai.model && !merged.includes(config.ai.model)) merged.unshift(config.ai.model)
      providers.value = providers.value.map((p) =>
        p.value === config.ai.provider ? { ...p, models: merged } : p
      )
      // 完整列表放进全局 store，报告面板的模型下拉直接使用
      appState.fetchedModels = merged
      if (!config.ai.model || !merged.includes(config.ai.model)) config.ai.model = merged[0]
      ElMessage.success(`获取到 ${res.models.length} 个模型`)
    } else {
      ElMessage.error(ipcErrorMessage(res?.message || res, '获取模型列表失败'))
    }
  } catch (err) {
    ElMessage.error('获取模型列表失败：' + ipcErrorMessage(err))
  } finally {
    fetchingModels.value = false
  }
}

// 选择工作区目录
const pickDirectory = async () => {
  picking.value = true
  try {
    const path = await api.pickDirectory()
    if (path) {
      const norm = path.replace(/\\/g, '/')
      if (!config.workspaces.includes(norm)) config.workspaces.push(norm)
    }
  } catch (err) {
    ElMessage.error('选择目录失败：' + ipcErrorMessage(err))
  } finally {
    picking.value = false
  }
}

// 通过输入框添加工作区
const addWorkspace = () => {
  const v = newWorkspace.value.trim().replace(/\\/g, '/')
  if (!v) return
  if (!config.workspaces.includes(v)) {
    config.workspaces.push(v)
  }
  newWorkspace.value = ''
}

// 移除工作区
const removeWorkspace = (idx) => {
  config.workspaces.splice(idx, 1)
}

const save = async () => {
  if (!loaded.value) {
    ElMessage.warning('配置尚未成功读取，请重新进入本页后再保存')
    return
  }
  if (!config.workspaces.length) {
    await ElMessageBox.alert('请至少添加一个工作区目录。', '无法保存', { type: 'warning' })
    return
  }
  saving.value = true
  try {
    // 只提交后端认识的字段，避免把响应式代理的额外内容带过去
    // apiKey 留空时后端保留旧值（保存后界面不回显明文）
    const saved = await api.saveConfig({
      ai: { ...config.ai },
      workspaces: [...config.workspaces],
      scan: { ...config.scan }
    })
    applyConfig(saved)
    apiKeyConfigured.value = !!saved?.ai?.apiKey || apiKeyConfigured.value
    config.ai.apiKey = ''
    ElMessage.success('配置已保存')
  } catch (err) {
    ElMessage.error('保存失败：' + ipcErrorMessage(err))
  } finally {
    saving.value = false
  }
}

const testConnection = async () => {
  testing.value = true
  try {
    const res = await api.testAi({ ...config.ai })
    if (res?.ok) {
      ElMessage.success(`连接成功（${res.latency}ms，模型 ${res.model}）`)
      if (res.baseUrl) effectiveBaseUrl.value = res.baseUrl
    } else {
      effectiveBaseUrl.value = res?.baseUrl || ''
      ElMessage.error('连接失败：' + ipcErrorMessage(res?.message))
    }
  } catch (err) {
    ElMessage.error('连接失败：' + ipcErrorMessage(err))
  } finally {
    testing.value = false
  }
}

const openConfigDir = async () => {
  try {
    await api.openConfigDir()
  } catch (err) {
    ElMessage.error(ipcErrorMessage(err, '打开配置文件位置失败'))
  }
}

onMounted(async () => {
  if (!runtimeReady()) {
    ElMessage.error('未检测到应用环境，请通过 wails dev 启动')
    return
  }
  await loadAppState()
  providers.value = appState.meta?.providers || []
  const saved = appState.config
  if (saved) {
    Object.assign(config.ai, saved.ai || {})
    // 不回显已保存的明文 Key：输入框留空表示「保持不变」
    apiKeyConfigured.value = !!saved.ai?.apiKey
    config.ai.apiKey = ''
    Object.assign(config.scan, saved.scan || {})
    config.workspaces = saved.workspaces || []
    loaded.value = true
  }
})
</script>

<template>
  <div class="page">
    <section class="card">
      <div class="card-head"><span class="card-title">大模型配置</span></div>
      <div class="card-body">
        <el-form :model="config.ai" label-width="130px" class="form">
          <el-form-item label="服务商">
            <el-select v-model="config.ai.provider" style="width: 260px" @change="onProviderChange">
              <el-option v-for="p in providers" :key="p.value" :label="p.label" :value="p.value" />
            </el-select>
            <span class="tip">切换服务商会自动填充 Base URL 与默认模型</span>
          </el-form-item>

          <el-form-item label="API Key">
            <el-input
              v-model="config.ai.apiKey"
              type="password"
              :placeholder="apiKeyConfigured ? '已保存（留空保持不变，输入新值可更换）' : 'sk-...'"
              autocomplete="new-password"
              style="width: 460px"
            />
            <span class="tip">保存后不再显示明文</span>
          </el-form-item>

          <el-form-item label="Base URL">
            <el-input v-model="config.ai.baseUrl" placeholder="https://api.deepseek.com/v1" style="width: 460px" />
          </el-form-item>

          <el-form-item label="Model">
            <div class="model-choose">
              <el-select
                v-model="config.ai.model"
                filterable
                allow-create
                default-first-option
                placeholder="选择或直接输入模型名"
                style="width: 320px"
              >
                <el-option v-for="m in currentModels" :key="m" :label="m" :value="m" />
              </el-select>
              <el-button :loading="fetchingModels" @click="fetchModels">
                <el-icon><Refresh /></el-icon>
                <span>获取模型列表</span>
              </el-button>
            </div>
            <span class="tip">切换服务商或输入 Base URL 后，可点击获取查询该地址下的全部模型</span>
          </el-form-item>

          <el-form-item label="Temperature">
            <el-slider v-model="config.ai.temperature" :min="0" :max="1" :step="0.1" style="width: 320px" />
            <span class="tip">{{ config.ai.temperature }}</span>
          </el-form-item>

          <el-form-item label="上下文上限">
            <el-input-number v-model="config.ai.maxChars" :min="2000" :max="200000" :step="2000" />
            <span class="tip">提交记录超出该字符数时只保留最新的部分</span>
          </el-form-item>

          <el-form-item>
            <el-button :loading="testing" @click="testConnection">
              <el-icon><Connection /></el-icon>
              <span>测试连接</span>
            </el-button>
            <el-button type="primary" :loading="saving" @click="save">
              <el-icon><Check /></el-icon>
              <span>保存配置</span>
            </el-button>
            <el-button link @click="openConfigDir">打开配置文件位置</el-button>
            <span v-if="effectiveBaseUrl" class="effective-url">实际请求：{{ effectiveBaseUrl }}</span>
          </el-form-item>
        </el-form>
      </div>
    </section>

    <section class="card">
      <div class="card-head"><span class="card-title">工作区</span></div>
      <div class="card-body">
        <div class="tip-block">添加要扫描的本地项目目录，应用会递归发现其中的 git 仓库并读取提交记录。</div>

        <div v-for="(ws, idx) in config.workspaces" :key="idx" class="workspace-item">
          <el-input :model-value="ws" readonly size="small" />
          <el-button size="small" type="danger" plain @click="removeWorkspace(idx)">移除</el-button>
        </div>

        <div class="workspace-item">
          <el-input v-model="newWorkspace" placeholder="输入工作区目录路径，如 D:\projects" size="small" />
          <el-button size="small" @click="addWorkspace">添加</el-button>
          <el-button size="small" :loading="picking" @click="pickDirectory">
            <el-icon><FolderOpened /></el-icon>
            <span>选择目录</span>
          </el-button>
        </div>
      </div>
    </section>

    <section class="card">
      <div class="card-head"><span class="card-title">扫描参数</span></div>
      <div class="card-body">
        <el-form :model="config.scan" label-width="130px" class="form">
          <el-form-item label="默认时间范围">
            <el-input-number v-model="config.scan.sinceDays" :min="0" :max="365" />
            <span class="tip">天；0 表示不限制</span>
          </el-form-item>
          <el-form-item label="最大扫描深度">
            <el-input-number v-model="config.scan.maxDepth" :min="1" :max="10" />
            <span class="tip">工作区下递归查找仓库的层级深度</span>
          </el-form-item>
          <el-form-item label="单仓库提交上限">
            <el-input-number v-model="config.scan.maxCommits" :min="50" :max="10000" :step="50" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="saving" @click="save">保存配置</el-button>
          </el-form-item>
        </el-form>
      </div>
    </section>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 920px;
}

.card {
  overflow: hidden;
}

.card-head {
  padding: 13px 20px;
  border-bottom: 1px solid #eef0f4;
}

.card-title {
  font-size: 14.5px;
  font-weight: 600;
  color: #1f2937;
}

.card-body {
  padding: 18px 20px;
}

.form :deep(.el-form-item) {
  margin-bottom: 16px;
}

.form :deep(.el-form-item:last-child) {
  margin-bottom: 0;
}

.tip {
  margin-left: 10px;
  font-size: 12px;
  color: #909399;
}

.model-choose {
  display: inline-flex;
  gap: 8px;
  align-items: center;
}

.effective-url {
  margin-left: 10px;
  font-size: 12px;
  color: #2563eb;
}

.tip-block {
  margin-bottom: 12px;
  font-size: 13px;
  color: #606266;
}

.tip-block code {
  padding: 1px 5px;
  background: #f2f3f5;
  border-radius: 4px;
}

.workspace-item {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
}

.row-actions {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}

</style>
