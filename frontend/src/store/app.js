/**
 * 全局共享状态：元信息与配置。
 * 顶栏的「模型已配置」状态、主界面默认时间范围、配置页表单都读这里，
 * 配置页保存后调用 loadAppState() 即可让顶栏状态同步刷新。
 */
import { computed, reactive } from 'vue'
import { api } from '../api'

export const appState = reactive({
  meta: {},
  config: null,
  /** 配置页「获取模型列表」拿到的完整模型列表，报告面板的下拉数据源 */
  fetchedModels: []
})

/** 模型是否可用（配置页填过 API Key 即为可用） */
export const aiReady = computed(() => !!appState.config?.ai?.apiKey)

/**
 * 模型下拉的候选项（报告面板与编辑弹窗共用同一份）：
 * 配置页 / 启动时抓到的全量模型列表优先，再并入当前服务商的内置预设，
 * 最后兜底补上配置里正在用的模型，保证当前模型一定在列表里。
 */
export const modelOptions = computed(() => {
  const ai = appState.config?.ai || {}
  const preset = (appState.meta?.providers || []).find((p) => p.value === ai.provider)
  const list = [...new Set([...(appState.fetchedModels || []), ...(preset?.models || [])])]
  if (ai.model && !list.includes(ai.model)) list.unshift(ai.model)
  return list
})

/** 同一个时刻只允许一个拉取任务，避免多处调用打出重复请求 */
let modelTask = null

/**
 * 主动查询当前 Base URL 下的全部模型并写入 appState.fetchedModels。
 * 失败时静默返回已有列表 —— 调用方是启动流程，不该因为网关不支持 /models 就弹错误。
 */
export async function loadModelOptions() {
  const ai = appState.config?.ai || {}
  if (!ai.apiKey && !ai.baseUrl) return appState.fetchedModels
  if (modelTask) return modelTask

  modelTask = api
    .listModels({})
    .then((res) => {
      if (res?.ok && res.models?.length) {
        const preset = (appState.meta?.providers || []).find((p) => p.value === ai.provider)
        const merged = [...new Set([...res.models, ...(preset?.models || [])])]
        if (ai.model && !merged.includes(ai.model)) merged.unshift(ai.model)
        appState.fetchedModels = merged
      }
      return appState.fetchedModels
    })
    .catch(() => appState.fetchedModels)
    .finally(() => {
      modelTask = null
    })
  return modelTask
}

/** 拉取元信息与配置；任何一项失败都返回已拿到的部分，避免整页报错 */
export async function loadAppState() {
  const [meta, config] = await Promise.all([
    api.getMeta().catch(() => null),
    api.getConfig().catch(() => null)
  ])
  if (meta) appState.meta = meta
  if (config) appState.config = config
  return appState.config
}

/** 保存配置后同步本地状态 */
export function applyConfig(config) {
  if (config) appState.config = config
}
