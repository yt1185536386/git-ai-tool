/**
 * 统一的调用层：把 Wails 自动生成的绑定包装成与 Electron 版一致的 api 形状，
 * 视图层只依赖这里，切换宿主环境时不用改视图代码。
 */
import * as App from '../../wailsjs/go/main/App'

/** 是否运行在 Wails 容器内（浏览器直接打开时为 false） */
export const hasBridge = () => typeof window !== 'undefined' && !!window.runtime

/** 界面是否可以正常调用后端：Wails 容器或浏览器预览时的 mock 都算 */
export const runtimeReady = () =>
  typeof window !== 'undefined' && (!!window.runtime || !!window.__DEV_MOCK__)

export const api = {
  /** 服务商预设、报告类型预设、配置文件位置、版本信息 */
  getMeta: () => App.GetMeta(),
  /** 读取配置 */
  getConfig: () => App.GetConfig(),
  /** 全量保存配置，返回落盘后的配置 */
  saveConfig: (config) => App.SaveConfig(config),
  /** 校验配置完整性：工作区 / API Key / 模型名缺一不可 */
  checkConfig: () => App.CheckConfig(),
  /** 扫描工作区下 git 仓库的提交记录 */
  scanGit: (payload) => App.ScanGit(payload),
  /** 调用 AI 生成报告（流式增量通过 ai:chunk 事件推送） */
  analyzeCommits: (payload) => App.AnalyzeCommits(payload),
  /** 停止正在进行的报告生成 */
  cancelAnalyze: () => App.CancelAnalyze(),
  /** 基于已有报告 + 用户需求做改写（流式增量通过 ai:refineChunk 事件推送） */
  refineReport: (payload) => App.RefineReport(payload),
  /** 停止正在进行的报告微调 */
  cancelRefine: () => App.CancelRefine(),
  /** 测试大模型连通性 */
  testAi: (ai) => App.TestAI(ai),
  /** 查询某 OpenAI 兼容地址下的全部模型 */
  listModels: (ai) => App.ListModels(ai),
  /** 选择工作区目录 */
  pickDirectory: () => App.PickDirectory(),
  /** 保存报告到本地文件 */
  saveReport: ({ filename, content }) => App.SaveReport(filename, content),
  /** 在资源管理器中定位配置文件 */
  openConfigDir: () => App.OpenConfigDir()
}
