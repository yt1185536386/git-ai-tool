/**
 * 浏览器预览用的 mock 绑定。
 *
 * 只有在「非 Wails 容器」里才会被 main.js 动态加载：用 vite build 出产物后
 * 直接丢进浏览器打开时需要它来喂数据，方便肉眼比对界面。
 * 打包进 Wails 应用后这段代码永远不会执行（那时存在 window.runtime）。
 *
 * 不需要浏览器预览时，删掉本文件并移除 main.js 里那三行动态 import 即可。
 */

const pad = (n) => String(n).padStart(2, '0')

/** 生成最近 N 天前的 ISO-8601 strict 时间串（与 git --date=iso-strict 一致） */
function daysAgo(days, hour, minute) {
  const d = new Date()
  d.setDate(d.getDate() - days)
  const offset = -d.getTimezoneOffset() / 60
  const sign = offset >= 0 ? '+' : '-'
  const abs = Math.abs(offset)
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(hour)}:${pad(minute)}:00${sign}${pad(Math.floor(abs))}:${pad(Math.round((abs % 1) * 60))}`
  )
}

const sampleCommits = [
  ['git-ai-tool', 'feat: 支持自定义时间范围扫描', 'main', false, 1, 0, 9, 21],
  ['git-ai-tool', 'fix: 修正分支归属推断时的重复计数', 'main', false, 1, 0, 11, 5],
  ['git-ai-tool', 'refactor: 抽出扫描参数校验', 'feat/scan-range', false, 1, 0, 14, 48],
  ['git-ai-tool', 'docs: 补充配置项说明', 'feat/scan-range', true, 3, 0, 16, 12],
  ['git-ai-tool', 'chore: 升级 element-plus 到 2.9', 'main', false, 2, 1, 10, 2],
  ['git-ai-tool', 'perf: 提交列表分片渲染，避免上千行卡顿', 'main', false, 1, 0, 15, 33],
  ['git-ai-tool', 'feat: 报告支持导出 Markdown', 'release/1.1', false, 1, 0, 18, 7],
  ['admin-portal', 'feat: 用户列表增加批量导出', 'main', false, 1, 1, 9, 45],
  ['admin-portal', 'fix: 修复分页在筛选后重置的问题', 'fix/pagination', false, 1, 0, 13, 26],
  ['admin-portal', 'style: 统一表格空态样式', 'main', false, 1, 2, 17, 9],
  ['admin-portal', 'refactor: 抽离权限判断到 composable', 'main', false, 4, 0, 11, 51],
  ['api-server', 'feat: 新增提交记录聚合接口', 'main', false, 2, 0, 10, 14],
  ['api-server', 'test: 补充 git 解析单测', 'main', false, 1, 0, 16, 40],
  ['api-server', 'fix: 空仓库扫描不再抛错', 'hotfix/empty-repo', true, 2, 1, 20, 3],
  ['api-server', 'chore: 移除未使用的依赖', 'main', false, 6, 0, 9, 12]
]

const commits = sampleCommits.map(([project, message, branch, branchInferred, branchCount, days, h, m], i) => {
  const hash = `${(i * 2654435761 % 0xffffffff).toString(16).padStart(8, '0')}`.slice(0, 40)
  return {
    hash,
    shortHash: hash.slice(0, 7),
    date: daysAgo(days, h, m),
    author: i % 5 === 3 ? '李四' : '张三',
    email: i % 5 === 3 ? 'lisi@example.com' : 'zhangsan@example.com',
    message,
    body: '',
    refs: `HEAD -> ${branch}`,
    branch,
    branchInferred,
    branchCount,
    project,
    repo: `D:\\projects\\${project}`
  }
})

const sampleReport = `## 本周主要工作
- **扫描能力**：新增自定义时间范围扫描，支持按起止日期精确筛选提交记录。
- **列表体验**：提交记录改为按天分组 + 分片渲染，上千条提交也能流畅滚动。
- **报告导出**：AI 报告支持一键导出 Markdown，方便直接贴进周会文档。

## 技术亮点
- 分支归属反查：\`git log --all\` 只在分支顶端带 ref，通过逐分支 log 建立 \`hash -> 分支\` 映射补齐归属。
- \`Base URL\` 自动补 \`/v1\`，规避 OpenAI 兼容网关返回首页 HTML 导致「模型返回为空」的坑。

## 风险与问题
- 分支数超过 30 个时放弃归属反查，此时分支列可能显示为空。
- Ollama 本地模型首次加载较慢，连接测试可能超时。

## 下周计划
- 接入增量扫描缓存，减少重复仓库的 log 开销。
- 报告模板支持自定义结构。`

const meta = {
  providers: [
    { value: 'openai', label: 'OpenAI', baseUrl: 'https://api.openai.com/v1', models: ['gpt-4o-mini', 'gpt-4o', 'gpt-4.1-mini'] },
    { value: 'deepseek', label: 'DeepSeek', baseUrl: 'https://api.deepseek.com/v1', models: ['deepseek-chat', 'deepseek-reasoner'] },
    { value: 'moonshot', label: 'Moonshot 月之暗面', baseUrl: 'https://api.moonshot.cn/v1', models: ['moonshot-v1-8k', 'moonshot-v1-32k'] },
    { value: 'zhipu', label: '智谱 GLM', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', models: ['glm-4-plus', 'glm-4-air'] },
    { value: 'qwen', label: '通义千问（兼容模式）', baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', models: ['qwen-plus', 'qwen-max'] },
    { value: 'ollama', label: 'Ollama 本地模型', baseUrl: 'http://127.0.0.1:11434/v1', models: ['qwen2.5:7b', 'llama3.1:8b'] },
    { value: 'custom', label: '自定义（OpenAI 兼容）', baseUrl: '', models: [] }
  ],
  promptPresets: [
    { value: 'weekly', label: '周报' },
    { value: 'daily', label: '日报' },
    { value: 'summary', label: '变更总结' },
    { value: 'release', label: '版本发布说明' },
    { value: 'custom', label: '自定义' }
  ],
  configPath: 'C:\\Users\\demo\\AppData\\Roaming\\GitAITool\\config.json',
  versions: { app: '1.1.1', wails: 'v2.10.1', go: 'go1.22.0', os: 'windows/amd64' }
}

let config = {
  ai: {
    provider: 'deepseek',
    apiKey: 'sk-demo-xxxxxxxxxxxxxxxx',
    baseUrl: 'https://api.deepseek.com/v1',
    model: 'deepseek-chat',
    temperature: 0.3,
    maxChars: 24000
  },
  workspaces: ['D:\\projects\\demo'],
  scan: { sinceDays: 7, maxDepth: 3, maxCommits: 800 }
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

/**
 * 最小事件总线：模拟 Wails 的 runtime.EventsOn / EventsOff / EventsEmit。
 * 有了它，浏览器预览也能看到「先等待、再流式上屏」的真实过程，
 * 而不是等一个 Promise 直接蹦出整篇报告。
 */
const listeners = new Map()
const mockRuntime = {
  EventsOn: (name, cb) => {
    if (!listeners.has(name)) listeners.set(name, new Set())
    listeners.get(name).add(cb)
  },
  EventsOff: (name) => listeners.delete(name),
  EventsEmit: (name, ...args) => {
    for (const cb of listeners.get(name) || []) cb(...args)
  }
}
window.runtime = mockRuntime

/** 把一段文本切成小片段推给前端，模拟流式输出 */
async function streamText(event, text, delay = 24) {
  for (const piece of text.match(/[\s\S]{1,16}/g) || []) {
    mockRuntime.EventsEmit(event, piece)
    await sleep(delay)
  }
}

const mockRepos = [
  { name: 'git-ai-tool', path: 'D:\\projects\\demo\\git-ai-tool', currentBranch: 'main', remote: 'https://github.com/example/git-ai-tool.git', remotes: [], commitCount: 7, error: '' },
  { name: 'admin-portal', path: 'D:\\projects\\admin-portal', currentBranch: 'main', remote: 'https://github.com/example/admin-portal.git', remotes: [], commitCount: 4, error: '' },
  { name: 'api-server', path: 'D:\\projects\\api-server', currentBranch: 'main', remote: 'https://github.com/example/api-server.git', remotes: [], commitCount: 4, error: '' }
]

let mockScanSeq = 0

const App = {
  GetMeta: () => Promise.resolve(meta),
  GetConfig: () => Promise.resolve(JSON.parse(JSON.stringify(config))),
  SaveConfig: (next) => {
    config = JSON.parse(JSON.stringify(next))
    return Promise.resolve(config)
  },
  CheckConfig: () =>
    Promise.resolve(
      !!config.workspaces?.length &&
        !!config.ai.apiKey?.trim() &&
        !!config.ai.model?.trim()
    ),
  ListModels: async () => {
    await sleep(500)
    return { ok: true, models: ['deepseek-chat', 'deepseek-coder', 'deepseek-reasoner'], baseUrl: config.ai.baseUrl, message: '' }
  },
  // 扫描：先推几条 scan:progress（模拟「查仓库 → 逐个仓库扫」），再返回结果。
  // 这样浏览器预览里也能看到进度条、跳秒和「最近扫描」概要。
  ScanGit: async () => {
    const seq = ++mockScanSeq
    const t0 = Date.now()
    const emit = (phase, reposDone, current, commits) =>
      mockRuntime.EventsEmit('scan:progress', {
        seq,
        phase,
        reposTotal: mockRepos.length,
        reposDone,
        current,
        commits,
        elapsedMs: Date.now() - t0
      })

    emit('discover', 0, '', 0)
    await sleep(300)
    emit('scan', 0, mockRepos[0].name, 0)
    let found = 0
    for (let i = 0; i < mockRepos.length; i++) {
      await sleep(560)
      found += mockRepos[i].commitCount
      emit('scan', i + 1, mockRepos[i + 1]?.name || mockRepos[i].name, found)
    }
    const elapsedMs = Date.now() - t0
    emit('done', mockRepos.length, '', found)
    return { commits, repos: mockRepos, warnings: [], seq, elapsedMs, cached: false, superseded: false }
  },
  PickDirectory: async () => {
    await sleep(200)
    return 'D:\\projects\\demo'
  },
  AnalyzeCommits: async () => {
    // 先等一会儿再吐第一个字：正好用来复现「模型生成中…」的等待态
    await sleep(900)
    await streamText('ai:chunk', sampleReport)
    return {
      content: sampleReport,
      meta: {
        provider: config.ai.provider,
        model: config.ai.model,
        baseUrl: config.ai.baseUrl,
        commitCount: 15,
        contextTruncated: false,
        contextUsed: 15,
        contextTotal: 15,
        range: '2026-09-10 ~ 2026-09-16',
        projects: ['git-ai-tool', 'admin-portal', 'api-server'],
        fromReasoning: false,
        usage: { prompt_tokens: 1284, completion_tokens: 386, total_tokens: 1670 }
      }
    }
  },
  RefineReport: async (payload) => {
    await sleep(600)
    const requirement = String(payload?.requirement || '').trim()
    const base = String(payload?.report || '').trimEnd()
    const content = `${base}\n\n> 已按需求调整：${requirement}`
    await streamText('ai:refineChunk', content)
    return {
      content,
      meta: {
        provider: config.ai.provider,
        model: payload?.aiOverride?.model || config.ai.model,
        baseUrl: config.ai.baseUrl,
        commitCount: 0,
        contextTruncated: false,
        contextUsed: 0,
        contextTotal: 0,
        range: '',
        projects: [],
        fromReasoning: false,
        usage: null
      }
    }
  },
  TestAI: async () => {
    await sleep(600)
    return { ok: true, latency: 412, model: config.ai.model, baseUrl: config.ai.baseUrl, reply: 'pong', message: '' }
  },
  CancelAnalyze: () => Promise.resolve(),
  CancelRefine: () => Promise.resolve(),
  SaveReport: (filename) => Promise.resolve({ ok: true, canceled: false, path: `D:\\reports\\${filename}` }),
  OpenConfigDir: () => Promise.resolve()
}

window.__DEV_MOCK__ = true
window.go = { main: { App } }
