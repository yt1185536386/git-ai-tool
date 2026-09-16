<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { ElMessage } from 'element-plus'
import { marked } from 'marked'
import { api, runtimeReady } from '../api'
import { appState, loadAppState, loadModelOptions, modelOptions } from '../store/app'
import { ipcErrorMessage } from '../utils/ipcError'

const scanning = ref(false)
const generating = ref(false)
const scanned = ref(false)

// 扫描进度：后端通过 scan:progress 事件推送「扫到第几个仓库 / 已发现多少条」，
// 耗时由前端自己的计时器走，事件不来也能一直跳，这样界面上始终有东西在动
const scanProgress = ref(null)
const scanElapsedMs = ref(0)
const lastScanInfo = ref(null)
let scanToken = 0 // 本地请求序号：丢弃「已被更新的扫描取代」的迟到结果
let lastProgressSeq = 0 // 已接受的最大进度序号：丢弃旧扫描继续推上来的进度
let scanTicker = null

const repos = ref([])
// 提交记录可能上千条：用 shallowRef 避免 Vue 逐条深度代理，
// 元素保持普通对象，跨边界序列化才通得过（Electron IPC 曾因此报 An object could not be cloned.）
const commits = shallowRef([])
const warnings = ref([])
const lastScanAt = ref('')

const sinceDays = ref(7)
const customRange = ref(false)
const customFrom = ref('')
const customTo = ref('')
let scanTimer = null

const selectedProjects = ref([])
const selectedAuthors = ref([])
const keyword = ref('')

/** 已勾选的提交 key 集合（用数组存，配一个 Set 做 O(1) 查询） */
const selectedKeys = ref([])
const selectedSet = computed(() => new Set(selectedKeys.value))

const promptType = ref('weekly')
const promptPresets = ref([
  { value: 'weekly', label: '周报' },
  { value: 'daily', label: '日报' },
  { value: 'summary', label: '变更总结' },
  { value: 'release', label: '发布说明' },
  { value: 'custom', label: '自定义' }
])
const customPrompt = ref('')

// AI 报告面板是否展开：默认隐藏，点击「AI 生成报告/更新生成」时从右侧滑入
const panelOpen = ref(false)
// AI 报告当前使用的模型，默认取配置里的模型，可在面板上切换
const selectedAiModel = ref('')

// 模型下拉的可选项来自全局 store（启动时自动拉一次 + 配置页「获取模型列表」的结果），
// 这里不再自己算，避免只回落到配置里的那一个模型
const refreshingModels = ref(false)

const refreshModels = async () => {
  refreshingModels.value = true
  try {
    const list = await loadModelOptions()
    if (list?.length) ElMessage.success(`已获取 ${list.length} 个模型`)
    else ElMessage.warning('未获取到模型列表，可到「系统配置」页手动获取')
  } finally {
    refreshingModels.value = false
  }
}

const report = ref('')
const reportMeta = ref(null)

// 流式生成：后端通过 ai:chunk 事件逐段推送增量，边收边渲染
const streamBuf = ref('')
const reportAreaRef = ref(null)

const scrollReportToBottom = () => {
  nextTick(() => {
    const el = reportAreaRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

// 监听后端流式增量。先解绑再注册，避免路由来回切换导致重复回调；
// 浏览器 mock 环境无 window.runtime，整段返回即可
if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
  window.runtime.EventsOff('ai:chunk')
  window.runtime.EventsOn('ai:chunk', (delta) => {
    if (!generating.value || typeof delta !== 'string' || !delta) return
    streamBuf.value += delta
    report.value = streamBuf.value
    scrollReportToBottom()
  })
  window.runtime.EventsOff('scan:progress')
  window.runtime.EventsOn('scan:progress', (p) => {
    if (!scanning.value || !p) return
    // 序号比自己见过的还小说明是上一次（已被顶掉）的扫描在继续上报，丢掉
    if (lastProgressSeq && p.seq < lastProgressSeq) return
    lastProgressSeq = p.seq || lastProgressSeq
    scanProgress.value = p
  })
}
onUnmounted(() => {
  window.runtime?.EventsOff?.('ai:chunk')
  window.runtime?.EventsOff?.('ai:refineChunk')
  window.runtime?.EventsOff?.('scan:progress')
  if (scanTicker) clearInterval(scanTicker)
})

/* ------------------------- 扫描进度展示 ------------------------- */

/** 扫描过程的实时文案：正在扫哪个仓库、扫到第几个、已发现多少条、耗时 */
const scanStatusText = computed(() => {
  const p = scanProgress.value
  if (!p || p.phase === 'discover') return '正在查找 git 仓库…'
  const parts = []
  const who = p.current ? `正在扫描 ${p.current}` : '正在扫描'
  parts.push(p.reposTotal > 1 ? `${who}（已扫完 ${p.reposDone}/${p.reposTotal} 个仓库）` : who)
  if (p.commits > 0) parts.push(`已发现 ${p.commits} 条提交`)
  parts.push(`${(scanElapsedMs.value / 1000).toFixed(1)}s`)
  return parts.join(' · ')
})

const startScanTicker = () => {
  scanElapsedMs.value = 0
  const t0 = Date.now()
  if (scanTicker) clearInterval(scanTicker)
  scanTicker = setInterval(() => {
    scanElapsedMs.value = Date.now() - t0
  }, 100)
}

const stopScanTicker = () => {
  if (scanTicker) {
    clearInterval(scanTicker)
    scanTicker = null
  }
}

/** 最近一次扫描的概要，显示在提交记录标题旁边 */
const lastScanText = computed(() => {
  const info = lastScanInfo.value
  if (!info) return ''
  const parts = [`扫描于 ${lastScanAt.value || '—'}`]
  if (info.repos) parts.push(`${info.repos} 个仓库`)
  parts.push(`${info.commits} 条提交`)
  if (info.ms) parts.push(`${(info.ms / 1000).toFixed(2)}s`)
  if (info.cached) parts.push('缓存命中')
  return parts.join(' · ')
})

const visibleCount = ref(150)

const rangeOptions = [
  { value: 1, label: '今天' },
  { value: 3, label: '近 3 天' },
  { value: 7, label: '近 7 天' },
  { value: 14, label: '近 14 天' },
  { value: 30, label: '近 30 天' },
  { value: 0, label: '全部' }
]

const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

/* ------------------------- 数据派生 ------------------------- */

// 同一次提交可能出现在多个仓库（fork），project + hash 才是唯一键
const commitKey = (c) => `${c.project}::${c.hash}`

const dayKeyOf = (iso) => {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return String(iso || '').slice(0, 10)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

const weekdayOf = (iso) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : WEEKDAYS[d.getDay()]
}

const timeOnly = (iso) => {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const projectOptions = computed(() => [...new Set(commits.value.map((c) => c.project))].sort())
const authorOptions = computed(() => [...new Set(commits.value.map((c) => c.author).filter(Boolean))].sort())

const filteredCommits = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return commits.value.filter((c) => {
    if (selectedProjects.value.length && !selectedProjects.value.includes(c.project)) return false
    if (selectedAuthors.value.length && !selectedAuthors.value.includes(c.author)) return false
    if (!kw) return true
    return (
      (c.message || '').toLowerCase().includes(kw) ||
      (c.author || '').toLowerCase().includes(kw) ||
      (c.project || '').toLowerCase().includes(kw) ||
      (c.shortHash || '').toLowerCase().includes(kw)
    )
  })
})

/** 只取当前筛选范围内、且被勾选的提交 —— 这就是喂给模型的输入 */
const selectedCommits = computed(() =>
  filteredCommits.value.filter((c) => selectedSet.value.has(commitKey(c)))
)

const activeProjectCount = computed(
  () => new Set(filteredCommits.value.map((c) => c.project)).size
)

const rangeText = computed(() => {
  if (!filteredCommits.value.length) return ''
  const dates = filteredCommits.value.map((c) => c.date).filter(Boolean).sort()
  if (!dates.length) return ''
  return `${dates[0].slice(0, 10)} ~ ${dates[dates.length - 1].slice(0, 10)}`
})

/** 按天分组（提交已按时间倒序，Map 保持插入顺序即可） */
const commitGroups = computed(() => {
  const groups = []
  const index = new Map()
  for (const c of filteredCommits.value) {
    const key = dayKeyOf(c.date)
    let g = index.get(key)
    if (!g) {
      g = { key, weekday: weekdayOf(c.date), items: [] }
      index.set(key, g)
      groups.push(g)
    }
    g.items.push(c)
  }
  for (const g of groups) {
    g.keys = g.items.map(commitKey)
    g.total = g.items.length
  }
  return groups
})

/** 渲染分片：一次最多进 DOM visibleCount 条，避免上千行卡顿 */
const renderedGroups = computed(() => {
  const out = []
  let left = visibleCount.value
  for (const g of commitGroups.value) {
    if (left <= 0) break
    const items = g.items.slice(0, left).map((c) => ({ ...c, key: commitKey(c) }))
    out.push({ key: g.key, weekday: g.weekday, total: g.total, keyList: g.keys, items })
    left -= items.length
  }
  return out
})

const renderedCount = computed(() =>
  renderedGroups.value.reduce((n, g) => n + g.items.length, 0)
)
const hiddenCount = computed(() => filteredCommits.value.length - renderedCount.value)

const dayBuckets = computed(() => {
  const map = new Map()
  for (const c of filteredCommits.value) {
    const k = dayKeyOf(c.date)
    map.set(k, (map.get(k) || 0) + 1)
  }
  return [...map.entries()]
    .sort((a, b) => (a[0] < b[0] ? 1 : -1))
    .slice(0, 14)
    .reverse()
    .map(([key, count]) => ({ key, count, label: key.slice(5) }))
})

const maxDayCount = computed(() => Math.max(1, ...dayBuckets.value.map((b) => b.count)))
const barHeight = (n) => `${Math.max(8, Math.round((n / maxDayCount.value) * 100))}%`

const reportHtml = computed(() => (report.value ? marked.parse(report.value) : ''))

const reportTitle = computed(() => {
  const found = promptPresets.value.find((p) => p.value === promptType.value)
  return `AI ${found?.label || '报告'}`
})

const branchTip = (row) => {
  if (row.branchCount > 1) {
    return `该提交同时存在于 ${row.branchCount} 个分支（仅显示第一个）`
  }
  return row.branchInferred ? '该提交未被分支/tag 直接指向，分支名为归属推断' : `分支 ${row.branch}`
}

/* ------------------------- 选择 ------------------------- */

const groupState = (keys) => {
  if (!keys.length) return 'none'
  let hit = 0
  for (const k of keys) if (selectedSet.value.has(k)) hit++
  if (hit === 0) return 'none'
  return hit === keys.length ? 'all' : 'some'
}

const toggle = (key) => {
  const next = [...selectedKeys.value]
  const i = next.indexOf(key)
  if (i >= 0) next.splice(i, 1)
  else next.push(key)
  selectedKeys.value = next
}

const toggleGroup = (keys) => {
  const all = groupState(keys) === 'all'
  const set = new Set(selectedKeys.value)
  for (const k of keys) {
    if (all) set.delete(k)
    else set.add(k)
  }
  selectedKeys.value = [...set]
}

const selectAllFiltered = () => {
  selectedKeys.value = filteredCommits.value.map(commitKey)
  ElMessage.success(`已选中 ${selectedKeys.value.length} 条`)
}

const clearSelection = () => {
  selectedKeys.value = []
}

/* ------------------------- 扫描 / 生成 ------------------------- */

const formatNow = () => {
  const d = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const scan = async () => {
  // 每次扫描领一个号：往返过程中如果又发起了新的扫描，这次的结果就作废
  const token = ++scanToken
  scanning.value = true
  scanProgress.value = null
  startScanTicker()
  report.value = ''
  reportMeta.value = null
  try {
    const res = await api.scanGit({
      sinceDays: customRange.value ? 0 : sinceDays.value,
      since: customRange.value ? customFrom.value : '',
      until: customRange.value ? customTo.value : ''
    })
    // 已经被更新的扫描取代：静默丢弃，别把旧数据写回界面
    if (token !== scanToken || res?.superseded) return
    const list = res?.commits || []
    commits.value = list
    repos.value = res?.repos || []
    warnings.value = res?.warnings || []
    selectedProjects.value = selectedProjects.value.filter((p) => projectOptions.value.includes(p))
    selectedAuthors.value = selectedAuthors.value.filter((a) => authorOptions.value.includes(a))
    // 扫描后默认全选，用户再按需取消勾选
    selectedKeys.value = list.map(commitKey)
    visibleCount.value = 150
    scanned.value = true
    lastScanAt.value = formatNow()
    lastScanInfo.value = {
      repos: repos.value.length,
      commits: list.length,
      ms: res?.elapsedMs || 0,
      cached: !!res?.cached
    }
    if (!list.length && !warnings.value.length) {
      ElMessage.info('该时间范围内没有提交记录')
    }
  } catch (err) {
    if (token !== scanToken) return
    ElMessage.error(`扫描失败：${ipcErrorMessage(err)}`)
  } finally {
    if (token === scanToken) {
      scanning.value = false
      scanProgress.value = null
      stopScanTicker()
    }
  }
}

/** 自定义日期改动后略作延迟再扫描，避免连改两个日期触发两次扫描 */
const scheduleScan = () => {
  if (scanTimer) clearTimeout(scanTimer)
  scanTimer = setTimeout(() => scan(), 350)
}

const setRange = (value) => {
  customRange.value = false
  sinceDays.value = value
  scan()
}

const enableCustomRange = () => {
  if (customRange.value) return
  const pad = (n) => String(n).padStart(2, '0')
  const today = new Date()
  const start = new Date(Date.now() - 6 * 86400000)
  const fmt = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  customFrom.value = fmt(start)
  customTo.value = fmt(today)
  customRange.value = true
  scan()
}

const generate = async () => {
  if (!selectedCommits.value.length) return
  panelOpen.value = true
  // 面板一打开就把模型列表备好，避免下拉里只有当前这一个模型
  if (!modelOptions.value.length) loadModelOptions()
  generating.value = true
  streamBuf.value = ''
  report.value = ''
  reportMeta.value = null
  try {
    // 跨边界传输只带后端用得到的字段，避免把响应式代理的额外字段带上
    const payloadCommits = selectedCommits.value.map((c) => ({
      hash: c.hash,
      shortHash: c.shortHash,
      date: c.date,
      author: c.author,
      email: c.email,
      message: c.message,
      branch: c.branch,
      project: c.project
    }))
    // 面板上可切换模型：用一份临时 AiOverride 覆盖模型名，其余沿用当前配置
    const aiBase = { ...(appState.config?.ai || {}) }
    const res = await api.analyzeCommits({
      commits: payloadCommits,
      promptType: promptType.value,
      customPrompt: customPrompt.value,
      aiOverride: selectedAiModel.value && selectedAiModel.value !== aiBase.model
        ? { ...aiBase, model: selectedAiModel.value }
        : undefined
    })
    // 流式过程中 report 已逐段更新，这里用后端返回的完整结果做最终一致化
    report.value = res?.content || streamBuf.value
    reportMeta.value = res?.meta || null
    ElMessage.success(`报告生成完成（基于 ${payloadCommits.length} 条提交）`)
  } catch (err) {
    const msg = ipcErrorMessage(err, 'AI 分析失败')
    if (msg.includes('停止') || msg.toLowerCase().includes('cancel')) {
      // 用户主动中断：保留已流式生成的部分内容
      if (streamBuf.value) report.value = streamBuf.value
      ElMessage.info('已停止生成，已生成的部分已保留')
    } else {
      ElMessage.error(msg)
    }
  } finally {
    generating.value = false
  }
}

const stopGenerate = async () => {
  try {
    await api.cancelAnalyze()
  } catch {
    // 后端取消后会以错误结束本次生成，这里忽略调用本身的异常
  }
}

const copyReport = async () => {
  try {
    await navigator.clipboard.writeText(report.value)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.error('复制失败，请手动选择文本')
  }
}

const saveReport = async () => {
  const stamp = new Date().toISOString().slice(0, 10)
  try {
    const res = await api.saveReport({
      filename: `git-report-${promptType.value}-${stamp}.md`,
      content: report.value
    })
    if (res?.ok) ElMessage.success('已保存到 ' + res.path)
  } catch (err) {
    ElMessage.error('导出失败：' + ipcErrorMessage(err))
  }
}

/* ------------------------- 报告编辑弹窗 ------------------------- */

// 弹窗是否打开
const editOpen = ref(false)
// 左侧可编辑的报告原文；模型改写的结果也实时回写到这里
const editContent = ref('')
// 右侧会话：[{ role: 'user' | 'assistant', content }]。弹窗级生命周期，关闭即清空
const editMessages = ref([])
// 输入框里的调整需求
const editRequirement = ref('')
// 弹窗内单独选择的模型（不影响面板上的选择）
const editModel = ref('')
// 是否正在改写
const refining = ref(false)
// 改写过程中的流式缓冲
const editBuf = ref('')
const editChatRef = ref(null)

// 会话序号：关闭弹窗或发起新请求后，旧请求的迟到回调一律丢弃，避免串到下一次会话里
let refineSeq = 0

const editTitle = computed(() => `${reportTitle.value}编辑`)

/* ------------------------- 提交详情弹窗 ------------------------- */

const detailOpen = ref(false)
const detailCommit = ref(null)

const openDetail = (c) => {
  detailCommit.value = c
  detailOpen.value = true
}

const detailTitle = computed(() =>
  detailCommit.value ? `${detailCommit.value.shortHash} 提交记录` : '提交记录'
)

/** 完整时间：YYYY-MM-DD HH:mm:ss + 星期 */
const fullTimeOf = (iso) => {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return String(iso || '')
  const pad = (n) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

const weekdayFullOf = (iso) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : WEEKDAYS[d.getDay()]
}

/** 详情里的「提交内容」= 首行说明 + 正文 */
const detailBody = computed(() => {
  const c = detailCommit.value
  if (!c) return ''
  return [c.message, c.body].filter((s) => String(s || '').trim()).join('\n\n')
})

const copyDetailHash = async () => {
  const hash = detailCommit.value?.hash
  if (!hash) return
  try {
    await navigator.clipboard.writeText(hash)
    ElMessage.success('已复制完整提交 ID')
  } catch {
    ElMessage.error('复制失败，请手动选择')
  }
}

const scrollChatToBottom = () => {
  nextTick(() => {
    const el = editChatRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

// 编辑弹窗的流式增量走独立事件，避免和报告生成的 ai:chunk 串台
if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
  window.runtime.EventsOff('ai:refineChunk')
  window.runtime.EventsOn('ai:refineChunk', (delta) => {
    if (!refining.value || typeof delta !== 'string' || !delta) return
    editBuf.value += delta
    editContent.value = editBuf.value
  })
}

const openEdit = () => {
  refineSeq += 1
  editContent.value = report.value
  // 会话只在弹窗存活期间有效：每次打开都从空白开始
  editMessages.value = []
  editRequirement.value = ''
  editBuf.value = ''
  refining.value = false
  editModel.value = selectedAiModel.value || appState.config?.ai?.model || ''
  editOpen.value = true
  // 模型列表还没拿到时补拉一次，保证下拉里有多个可选模型
  if (!modelOptions.value.length) loadModelOptions()
}

/** 清空本次弹窗的会话状态（关闭 / 保存后调用） */
const resetEditSession = () => {
  refineSeq += 1
  refining.value = false
  editMessages.value = []
  editRequirement.value = ''
  editBuf.value = ''
}

/** 取消：直接关闭，并清空会话 */
const closeEdit = () => {
  editOpen.value = false
  if (refining.value) {
    // 后端中断后会以错误结束本次调用，这里忽略取消请求本身的异常
    api.cancelRefine().catch(() => {})
  }
  resetEditSession()
}

/** 保存：把编辑框内容回写到「AI 周报」面板后关闭 */
const saveEdit = () => {
  report.value = editContent.value
  editOpen.value = false
  resetEditSession()
  ElMessage.success('已更新报告内容')
}

const stopRefine = async () => {
  try {
    await api.cancelRefine()
  } catch {
    // 与停止生成同理：中断的报错由 runRefine 统一处理
  }
}

const runRefine = async () => {
  if (refining.value) return
  const requirement = editRequirement.value.trim()
  if (!requirement) {
    ElMessage.info('请先输入调整需求')
    return
  }
  if (!editContent.value.trim()) {
    ElMessage.info('报告内容为空，无法调整')
    return
  }

  const seq = ++refineSeq
  refining.value = true
  editBuf.value = ''
  // 历史交给后端拼进 messages，让「再精简一点」这类指代上一轮的指令能接上上下文
  const history = editMessages.value.map((m) => ({ role: m.role, content: m.content }))
  editMessages.value = [...editMessages.value, { role: 'user', content: requirement }]
  editRequirement.value = ''
  scrollChatToBottom()

  try {
    const aiBase = { ...(appState.config?.ai || {}) }
    const res = await api.refineReport({
      report: editContent.value,
      requirement,
      history,
      promptType: promptType.value,
      aiOverride:
        editModel.value && editModel.value !== aiBase.model
          ? { ...aiBase, model: editModel.value }
          : undefined
    })
    if (seq !== refineSeq) return
    editContent.value = res?.content || editBuf.value
    const model = res?.meta?.model || editModel.value || '模型'
    editMessages.value = [
      ...editMessages.value,
      {
        role: 'assistant',
        content: `已按需求调整（${model}，共 ${editContent.value.length} 字），确认无误后点「保存」写回报告。`
      }
    ]
  } catch (err) {
    if (seq !== refineSeq) return
    const msg = ipcErrorMessage(err, 'AI 调整失败')
    if (msg.includes('停止') || msg.toLowerCase().includes('cancel')) {
      // 用户主动终止：保留已经流式出来的部分
      if (editBuf.value) editContent.value = editBuf.value
      editMessages.value = [
        ...editMessages.value,
        { role: 'assistant', content: '已终止执行，保留当前已生成的内容。' }
      ]
    } else {
      editMessages.value = [...editMessages.value, { role: 'assistant', content: '调整失败：' + msg }]
      ElMessage.error(msg)
    }
  } finally {
    if (seq === refineSeq) {
      refining.value = false
      editBuf.value = ''
    }
    scrollChatToBottom()
  }
}

onMounted(async () => {
  if (!runtimeReady()) {
    ElMessage.error('未检测到应用环境，请通过 wails dev 启动')
    return
  }
  const meta = appState.meta.versions ? appState.meta : await loadAppState()
  if (meta?.promptPresets?.length) {
    const list = [...meta.promptPresets]
    if (!list.some((p) => p.value === 'custom')) list.push({ value: 'custom', label: '自定义' })
    promptPresets.value = list
  }
  const config = appState.config
  if (config?.scan?.sinceDays !== undefined) sinceDays.value = config.scan.sinceDays
  selectedAiModel.value = config?.ai?.model || ''
  // 已配置工作区时自动扫描一次
  if (config?.workspaces?.length) await scan()
})
</script>

<template>
  <div class="page">
    <!-- ============ 过滤条 ============ -->
    <section class="card filter-card">
      <div class="filter-row">
        <div class="range-tabs">
          <button
            v-for="opt in rangeOptions"
            :key="opt.value"
            type="button"
            class="range-tab"
            :class="{ active: !customRange && sinceDays === opt.value }"
            @click="setRange(opt.value)"
          >
            {{ opt.label }}
          </button>
          <button
            type="button"
            class="range-tab"
            :class="{ active: customRange }"
            @click="enableCustomRange"
          >
            自定义
          </button>
        </div>

        <div v-if="customRange" class="range-picker">
          <el-date-picker
            v-model="customFrom"
            type="date"
            value-format="YYYY-MM-DD"
            placeholder="开始日期"
            :clearable="false"
            size="small"
            style="width: 136px"
            @change="scheduleScan"
          />
          <span class="tilde">至</span>
          <el-date-picker
            v-model="customTo"
            type="date"
            value-format="YYYY-MM-DD"
            placeholder="结束日期"
            :clearable="false"
            size="small"
            style="width: 136px"
            @change="scheduleScan"
          />
        </div>
        <div v-else class="range-readonly">
          <el-icon><Calendar /></el-icon>
          <span>{{ rangeText || '尚未扫描' }}</span>
        </div>

        <div class="spacer"></div>

        <el-select
          v-model="selectedProjects"
          multiple
          collapse-tags
          collapse-tags-tooltip
          placeholder="全部项目"
          style="width: 172px"
        >
          <el-option v-for="p in projectOptions" :key="p" :label="p" :value="p" />
        </el-select>

        <el-select
          v-model="selectedAuthors"
          multiple
          collapse-tags
          collapse-tags-tooltip
          placeholder="全部作者"
          style="width: 150px"
        >
          <el-option v-for="a in authorOptions" :key="a" :label="a" :value="a" />
        </el-select>

        <el-input v-model="keyword" placeholder="搜索提交信息" clearable style="width: 176px">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>

        <el-button type="primary" :loading="scanning" @click="scan">
          <el-icon v-if="!scanning"><Refresh /></el-icon>
          <span>扫描提交记录</span>
        </el-button>
      </div>

      <div class="filter-row second">
        <span class="field-label">报告类型</span>
        <el-select v-model="promptType" style="width: 138px">
          <el-option v-for="p in promptPresets" :key="p.value" :label="p.label" :value="p.value" />
        </el-select>
        <el-input
          v-if="promptType === 'custom'"
          v-model="customPrompt"
          placeholder="自定义提示词，例如：只统计 feat / fix 提交，并按模块归类"
          style="width: 380px"
        />

        <div class="spacer"></div>

        <el-button v-if="report && !panelOpen" @click="panelOpen = true">
          <el-icon><Document /></el-icon>
          <span>查看报告</span>
        </el-button>

        <span v-if="filteredCommits.length" class="gen-hint">
          将基于已勾选的 <b>{{ selectedCommits.length }}</b> / {{ filteredCommits.length }} 条提交生成
        </span>
        <!-- 生成与停止互斥：生成中隐藏生成按钮，只留红色的停止；结束/中断后生成按钮回来 -->
        <el-button
          v-if="!generating"
          type="success"
          :disabled="!selectedCommits.length"
          @click="generate"
        >
          <el-icon><MagicStick /></el-icon>
          <span>AI 生成报告</span>
        </el-button>
        <el-button v-else type="danger" @click="stopGenerate">
          <el-icon><VideoPause /></el-icon>
          <span>停止生成</span>
        </el-button>
      </div>
    </section>

    <!-- ============ 统计卡 ============ -->
    <section class="stat-row">
      <div class="card stat">
        <div class="stat-num">{{ filteredCommits.length }}</div>
        <div class="stat-label">提交</div>
      </div>
      <div class="card stat">
        <div class="stat-num">{{ activeProjectCount }}</div>
        <div class="stat-label">仓库</div>
      </div>
      <div class="card stat stat-wide">
        <div class="stat-head">
          <span class="stat-label">提交分布</span>
          <span class="stat-sub">{{ dayBuckets.length }} 天</span>
        </div>
        <div v-if="dayBuckets.length" class="spark">
          <div
            v-for="b in dayBuckets"
            :key="b.key"
            class="spark-col"
            :title="`${b.key} · ${b.count} 条`"
          >
            <div class="spark-track">
              <div class="spark-bar" :style="{ height: barHeight(b.count) }"></div>
            </div>
            <span class="spark-label">{{ b.label }}</span>
          </div>
        </div>
        <div v-else class="spark-empty">暂无数据</div>
      </div>
    </section>

    <el-alert
      v-if="warnings.length"
      type="warning"
      show-icon
      :closable="false"
      class="warn-alert"
      title="扫描提示"
    >
      <ul class="warning-list">
        <li v-for="(w, i) in warnings" :key="i">{{ w }}</li>
      </ul>
    </el-alert>

    <!-- ============ 主体两栏 ============ -->
    <div class="columns">
      <!-- 左：提交记录（AI 面板关闭时占满宽度） -->
      <section class="card column left-column">
        <div class="card-head">
          <div class="card-title">
            提交记录
            <span class="muted">共 {{ filteredCommits.length }} 条</span>
            <span v-if="lastScanText" class="scan-meta">{{ lastScanText }}</span>
          </div>
          <div class="head-actions">
            <span class="sel-count">已选 {{ selectedCommits.length }}</span>
            <button type="button" class="mini-btn" @click="selectAllFiltered">全选</button>
            <button type="button" class="mini-btn" :disabled="!selectedKeys.length" @click="clearSelection">
              清空
            </button>
          </div>
        </div>

        <div class="commit-scroll">
          <!-- 扫描进度：贴在列表顶部，滚动时也保持可见。列表还空着时改用下面的居中等待态 -->
          <div v-if="scanning && filteredCommits.length" class="scan-strip">
            <span class="scan-bar"><i></i></span>
            <span class="scan-text">{{ scanStatusText }}</span>
          </div>

          <div v-for="group in renderedGroups" :key="group.key" class="day-group">
            <div class="day-head">
              <span class="check-wrap" @click.stop>
                <el-checkbox
                  :model-value="groupState(group.keyList) === 'all'"
                  :indeterminate="groupState(group.keyList) === 'some'"
                  @change="toggleGroup(group.keyList)"
                />
              </span>
              <span class="day-date">{{ group.key }}</span>
              <span class="day-week">{{ group.weekday }}</span>
              <span class="day-count">{{ group.total }} 条</span>
            </div>

            <div
              v-for="c in group.items"
              :key="c.key"
              class="commit-row"
              :class="{ checked: selectedSet.has(c.key) }"
              @click="toggle(c.key)"
            >
              <span class="check-wrap" @click.stop>
                <el-checkbox :model-value="selectedSet.has(c.key)" @change="toggle(c.key)" />
              </span>
              <span class="c-time">{{ timeOnly(c.date) }}</span>
              <span class="c-project" :title="c.repo">{{ c.project }}</span>
              <el-tooltip v-if="c.branch" :content="branchTip(c)" placement="top" :show-after="400">
                <span class="c-branch" :class="{ inferred: c.branchInferred }">
                  {{ c.branch }}<em v-if="c.branchCount > 1">+{{ c.branchCount - 1 }}</em>
                </span>
              </el-tooltip>
              <span v-else class="c-branch empty">—</span>
              <!-- 提交说明可点开详情；stop 避免触发整行的勾选切换 -->
              <span
                class="c-msg clickable"
                title="点击查看提交详情"
                @click.stop="openDetail(c)"
              >{{ c.message }}</span>
              <span class="c-author" :title="c.email">{{ c.author }}</span>
              <span class="c-hash">{{ c.shortHash }}</span>
            </div>
          </div>

          <!-- 扫描中且还没有数据时，用和 AI 等待态一致的呼吸点，别让面板空着 -->
          <div v-if="!filteredCommits.length && scanning" class="scan-empty">
            <div class="gen-dots"><i></i><i></i><i></i></div>
            <p>{{ scanStatusText }}</p>
          </div>

          <el-empty
            v-else-if="!filteredCommits.length"
            :description="scanned ? '当前条件下没有提交记录' : '点击右上角「扫描提交记录」开始'"
            :image-size="80"
          />

          <div v-if="hiddenCount > 0" class="more-row">
            <el-button link type="primary" @click="visibleCount += 150">
              加载更多（剩余 {{ hiddenCount }} 条）
            </el-button>
          </div>
        </div>
      </section>

      <!-- 右：AI 报告（面板，默认隐藏，从右侧滑入滑出） -->
      <section class="card column report-column" :class="{ open: panelOpen }">
        <!-- 等待遮罩：只在「还没有任何内容」的等待阶段盖住卡片；
             首个流式分片一到（streamBuf 非空）就撤掉，让内容边收边显示，
             不再用一个突兀的遮罩把整个面板压住 -->
        <div v-if="generating && !streamBuf" class="gen-mask">
          <div class="gen-dots"><i></i><i></i><i></i></div>
          <div class="gen-text">模型生成中…</div>
        </div>

        <div class="card-head">
          <div class="card-title">{{ reportTitle }}</div>
          <div class="model-picker">
            <el-select
              v-model="selectedAiModel"
              class="plain-select"
              size="small"
              filterable
              allow-create
              default-first-option
              placeholder="选择模型"
              style="width: 126px"
            >
              <el-option v-for="m in modelOptions" :key="m" :label="m" :value="m" />
            </el-select>
            <el-button
              size="small"
              text
              :loading="refreshingModels"
              title="重新获取该接口下的模型列表"
              @click="refreshModels"
            >
              <el-icon v-if="!refreshingModels"><Refresh /></el-icon>
            </el-button>
          </div>
          <div class="head-actions">
            <el-button size="small" :disabled="!report" @click="openEdit">
              <el-icon><Edit /></el-icon>
              <span>编辑</span>
            </el-button>
            <el-button
              v-if="!generating"
              size="small"
              :disabled="!selectedCommits.length"
              @click="generate"
            >
              <el-icon><MagicStick /></el-icon>
              <span>{{ report ? '更新生成' : '生成' }}</span>
            </el-button>
            <el-button size="small" :disabled="!report" @click="copyReport">
              <el-icon><CopyDocument /></el-icon>
              <span>复制</span>
            </el-button>
            <el-button size="small" :disabled="!report" @click="saveReport">
              <el-icon><Download /></el-icon>
              <span>导出</span>
            </el-button>
          </div>
        </div>

        <!-- 流式进行中的状态就放在原元信息那一行，不用遮罩 -->
        <div v-if="generating && streamBuf" class="report-meta-line streaming">
          <span class="dot-flow"></span>
          模型生成中… 已生成 <b>{{ streamBuf.length }}</b> 字
        </div>

        <div v-if="reportMeta" class="report-meta-line">
          覆盖 <b>{{ reportMeta.commitCount }}</b> 条提交、{{ reportMeta.projects?.length || 0 }} 个项目
          <span v-if="reportMeta.contextTruncated" class="warn">
            · 上下文已截断（{{ reportMeta.contextUsed }}/{{ reportMeta.contextTotal }}）
          </span>
        </div>

        <div ref="reportAreaRef" class="report-area">
          <div v-if="report" class="markdown-body" v-html="reportHtml"></div>
          <div v-else-if="generating" class="placholder"></div>
          <el-empty v-else description="勾选提交记录后点击「AI 生成报告」" :image-size="80" />
        </div>

        <div v-if="reportMeta" class="report-foot">
          <button
            type="button"
            class="foot-close"
            title="收起 AI 报告面板"
            @click="panelOpen = false"
          >
            <el-icon><ArrowRight /></el-icon>
          </button>
          <span>{{ reportMeta.model }}</span>
          <span v-if="reportMeta.range">· {{ reportMeta.range }}</span>
          <span v-if="reportMeta.usage">· tokens {{ reportMeta.usage.total_tokens }}</span>
        </div>
      </section>
    </div>

    <!-- ============ 提交详情弹窗 ============ -->
    <el-dialog
      v-model="detailOpen"
      :title="detailTitle"
      width="560px"
      top="12vh"
      class="commit-dialog"
      :close-on-click-modal="true"
    >
      <div v-if="detailCommit" class="commit-detail">
        <div class="cd-row">
          <span class="cd-label">项目</span>
          <span class="cd-value" :title="detailCommit.repo">{{ detailCommit.project }}</span>
        </div>
        <div class="cd-row">
          <span class="cd-label">分支</span>
          <span class="cd-value">
            <template v-if="detailCommit.branch">
              {{ detailCommit.branch }}
              <em v-if="detailCommit.branchCount > 1" class="cd-extra">
                +{{ detailCommit.branchCount - 1 }}
              </em>
              <em v-else-if="detailCommit.branchInferred" class="cd-extra">推断</em>
            </template>
            <span v-else class="cd-empty">—</span>
          </span>
        </div>
        <div class="cd-row">
          <span class="cd-label">作者</span>
          <span class="cd-value" :title="detailCommit.email">
            {{ detailCommit.author }}
            <em v-if="detailCommit.email" class="cd-mail">&lt;{{ detailCommit.email }}&gt;</em>
          </span>
        </div>
        <div class="cd-row">
          <span class="cd-label">时间</span>
          <span class="cd-value">
            {{ fullTimeOf(detailCommit.date) }}
            <em v-if="weekdayFullOf(detailCommit.date)" class="cd-extra">
              {{ weekdayFullOf(detailCommit.date) }}
            </em>
          </span>
        </div>
        <div class="cd-row">
          <span class="cd-label">提交 ID</span>
          <span class="cd-value cd-hash">
            <code :title="detailCommit.hash">{{ detailCommit.shortHash }}</code>
            <el-icon class="cd-copy" title="复制完整提交 ID" @click="copyDetailHash">
              <CopyDocument />
            </el-icon>
          </span>
        </div>
        <div class="cd-row cd-content">
          <span class="cd-label">提交内容</span>
          <div class="cd-msg">{{ detailBody || '（无提交说明）' }}</div>
        </div>
      </div>

      <template #footer>
        <el-button @click="detailOpen = false">
          <el-icon><Close /></el-icon>
          <span>关闭</span>
        </el-button>
      </template>
    </el-dialog>

    <!-- ============ 报告编辑弹窗 ============ -->
    <el-dialog
      v-model="editOpen"
      :title="editTitle"
      width="1040px"
      top="5vh"
      class="edit-dialog"
      :close-on-click-modal="false"
      @closed="resetEditSession"
    >
      <div class="edit-body">
        <!-- 左：报告原文，可直接手改 -->
        <section class="edit-pane">
          <div class="pane-head">
            <span>报告原文</span>
            <span class="pane-tip">可直接手动修改</span>
          </div>
          <el-input
            v-model="editContent"
            type="textarea"
            :rows="20"
            resize="none"
            :readonly="refining"
            placeholder="报告内容"
            class="edit-textarea"
          />
        </section>

        <!-- 右：AI 调整会话 -->
        <section class="edit-pane">
          <div class="pane-head">
            <span>AI 调整助手</span>
            <span class="pane-tip">会话仅在本次弹窗内有效</span>
          </div>

          <div ref="editChatRef" class="chat-list">
            <div v-if="!editMessages.length && !refining" class="chat-empty">
              说清你想怎么改即可，例如「把「风险与问题」并进「本周主要工作」」「语气更正式一些」
              「按模块重新归类」。模型会基于左侧内容改写，结果直接覆盖左侧文本。
            </div>
            <div v-for="(m, i) in editMessages" :key="i" class="chat-item" :class="m.role">
              <div class="chat-role" :class="m.role">{{ m.role === 'user' ? '我' : '优' }}</div>
              <div class="chat-bubble">{{ m.content }}</div>
            </div>
            <div v-if="refining" class="chat-item assistant">
              <div class="chat-role assistant">优</div>
              <div class="chat-bubble typing">
                <span class="typing-text">小优正在按需求调整</span>
                <span class="typing-dots"><i></i><i></i><i></i></span>
              </div>
            </div>
          </div>

          <div class="chat-input">
            <el-input
              v-model="editRequirement"
              type="textarea"
              :rows="3"
              resize="none"
              :disabled="refining"
              placeholder="输入调整需求，Ctrl + Enter 直接生成"
              @keydown.ctrl.enter.prevent="runRefine"
            />
            <div class="chat-toolbar">
              <el-select
                v-model="editModel"
                class="plain-select"
                size="small"
                filterable
                allow-create
                default-first-option
                :disabled="refining"
                placeholder="选择模型"
                style="width: 130px"
              >
                <el-option v-for="m in modelOptions" :key="m" :label="m" :value="m" />
              </el-select>
              <div class="spacer"></div>
              <el-button v-if="!refining" type="primary" size="small" @click="runRefine">
                <el-icon><MagicStick /></el-icon>
                <span>生成</span>
              </el-button>
              <el-button v-else type="danger" size="small" @click="stopRefine">
                <el-icon><VideoPause /></el-icon>
                <span>终止</span>
              </el-button>
            </div>
          </div>
        </section>
      </div>

      <template #footer>
        <el-button @click="closeEdit">
          <el-icon><Close /></el-icon>
          <span>取消</span>
        </el-button>
        <el-button type="primary" :disabled="!editContent.trim()" @click="saveEdit">
          <el-icon><Check /></el-icon>
          <span>保存</span>
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  /* 撑满可视区域，让「提交记录 / AI 报告」两栏自己吃满剩余空间，
     只有列表内部滚动，整页不出现滚动条 */
  height: calc(100vh - 90px);
  min-height: 560px;
}

/* ---------- 过滤条 ---------- */
.filter-card {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: 12px;
  padding: 16px 18px;
}

.filter-row {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}

.filter-row.second {
  padding-top: 12px;
  border-top: 1px dashed #eef0f4;
}

.spacer {
  flex: 1;
  min-width: 0;
}

.field-label {
  font-size: 13px;
  color: #6b7280;
}

.range-tabs {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  background: #f3f5f9;
  border-radius: 9px;
}

.range-tab {
  padding: 6px 13px;
  font-family: inherit;
  font-size: 13px;
  color: #5b6472;
  cursor: pointer;
  background: transparent;
  border: none;
  border-radius: 7px;
  transition: all 0.15s;
}

.range-tab:hover {
  color: #2563eb;
}

.range-tab.active {
  font-weight: 600;
  color: #2563eb;
  background: #fff;
  box-shadow: 0 1px 3px rgb(31 41 55 / 10%);
}

.range-picker {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}

.tilde {
  font-size: 12px;
  color: #9aa3b2;
}

.range-readonly {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  padding: 5px 11px;
  font-size: 12.5px;
  color: #6b7280;
  background: #f7f8fb;
  border-radius: 7px;
}

.gen-hint {
  font-size: 12.5px;
  color: #6b7280;
}

.gen-hint b {
  color: #2563eb;
}

/* ---------- 统计卡 ---------- */
.stat-row {
  display: grid;
  flex: none;
  grid-template-columns: 130px 130px minmax(0, 1fr);
  gap: 12px;
}

.stat {
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 14px 18px;
}

.stat-num {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.15;
  color: #1f2937;
}

.stat-label {
  font-size: 12.5px;
  color: #8a93a2;
}

.stat-wide {
  justify-content: flex-start;
  padding: 12px 18px;
}

.stat-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 8px;
}

.stat-sub {
  font-size: 12px;
  color: #a8b0bd;
}

.spark {
  display: flex;
  gap: 6px;
  height: 58px;
}

.spark-col {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.spark-track {
  display: flex;
  flex: 1;
  align-items: flex-end;
  justify-content: center;
  min-height: 0;
}

.spark-bar {
  width: 100%;
  max-width: 30px;
  min-height: 3px;
  background: linear-gradient(180deg, #60a5fa, #2563eb);
  border-radius: 4px 4px 2px 2px;
}

.spark-label {
  font-size: 10px;
  color: #a8b0bd;
  text-align: center;
  white-space: nowrap;
}

.spark-empty {
  font-size: 12px;
  color: #b6bcc6;
}

.warn-alert {
  border-radius: 10px;
}

.warning-list {
  padding-left: 18px;
  margin: 0;
  font-size: 13px;
  line-height: 1.9;
}

/* ---------- 两栏 ---------- */
.columns {
  display: flex;
  flex: 1;
  gap: 0;
  min-height: 0;
  /* 报告栏收起后会缩到 0 宽，此时任何一点点视觉溢出（阴影、位移动画）都会把
     .app-main 撑出横向滚动条；横向滚动条一出现就吃掉 8px 高度，又让整页
     （height: calc(100vh - 90px)）多出 8px 触发纵向滚动条，来回抖动。
     这里直接裁掉溢出，从根上断掉这个循环 */
  overflow: hidden;
}

.column {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: 0;
  overflow: hidden;
}

/* 左列：AI 面板关闭时占满整宽 */
.left-column {
  flex: 1;
  min-width: 0;
}

/* 右列：默认收起（宽度收到 0），.open 时展开 */
.report-column {
  position: relative;
  flex: 0 0 auto;
  width: 0;
  min-width: 0;
  opacity: 0;
  /* 不用 translateX 做滑入：收起后元素停在容器右缘外侧 24px，
     会形成滚动溢出（详见 .columns 的注释）。宽度本身就在做动画，视觉够了 */
  transition: width 0.28s ease, margin-left 0.28s ease, opacity 0.2s ease;
}

/* 生成等待遮罩：绝对定位铺满整个报告卡片，只在「第一个字还没到」时出现。
   做成很轻的一层雾 + 三个呼吸点，避免一块硬白板和粗描边文字怼在脸上 */
.gen-mask {
  position: absolute;
  inset: 0;
  z-index: 20;
  display: flex;
  flex-direction: column;
  gap: 14px;
  align-items: center;
  justify-content: center;
  background: rgb(255 255 255 / 55%);
  backdrop-filter: blur(3px);
  animation: mask-in 0.32s ease both;
}

@keyframes mask-in {
  from {
    opacity: 0;
  }

  to {
    opacity: 1;
  }
}

.gen-dots {
  display: flex;
  gap: 7px;
}

.gen-dots i {
  width: 7px;
  height: 7px;
  background: #9dbaf3;
  border-radius: 50%;
  animation: dot-breathe 1.2s ease-in-out infinite;
}

.gen-dots i:nth-child(2) {
  animation-delay: 0.15s;
}

.gen-dots i:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes dot-breathe {
  0%,
  75%,
  100% {
    opacity: 0.3;
    transform: translateY(0) scale(0.8);
  }

  35% {
    opacity: 1;
    transform: translateY(-4px) scale(1);
  }
}

.gen-text {
  font-size: 12.5px;
  font-weight: 400;
  color: #a3aebd;
  letter-spacing: 0.4px;
}

/* 报告面板标题后的模型选择：下拉 + 重新获取按钮 */
.model-picker {
  display: flex;
  gap: 2px;
  align-items: center;
}

.model-picker .el-button {
  padding: 0 4px;
  color: #a8b0bd;
}

/* 无边框下拉（报告面板头部 / 编辑弹窗工具条）。
   Element 的边框是 .el-select__wrapper 上的 box-shadow 而不是 border，
   所以要连 hover / focus 的 box-shadow 一起覆盖掉 */
.plain-select :deep(.el-select__wrapper) {
  padding: 0 2px;
  background: transparent;
  box-shadow: none;
}

.plain-select :deep(.el-select__wrapper:hover),
.plain-select :deep(.el-select__wrapper.is-hovering),
.plain-select :deep(.el-select__wrapper.is-focused) {
  box-shadow: none;
}

.report-column.open {
  /* 面板头部除了标题还有「模型下拉 + 编辑/生成/复制/导出」，48% 以下会挤成两行 */
  width: 52%;
  margin-left: 14px;
  opacity: 1;
}

.card-head {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  gap: 8px 12px;
  align-items: center;
  padding: 13px 18px;
  border-bottom: 1px solid #eef0f4;
}

/* 标题吃掉剩余空间，把模型下拉与操作按钮一起顶到右边 */
.card-title {
  display: flex;
  flex: 1 1 auto;
  gap: 8px;
  align-items: baseline;
  min-width: 0;
  font-size: 14.5px;
  font-weight: 600;
  color: #1f2937;
}

.head-actions {
  display: flex;
  gap: 6px;
  align-items: center;
  /* 头部换行时（窄窗口）让按钮仍然贴右 */
  margin-left: auto;
}

.sel-count {
  margin-right: 2px;
  font-size: 12.5px;
  color: #2563eb;
}

.mini-btn {
  padding: 3px 9px;
  font-family: inherit;
  font-size: 12px;
  color: #5b6472;
  cursor: pointer;
  background: #f4f6fa;
  border: none;
  border-radius: 6px;
  transition: all 0.15s;
}

.mini-btn:hover:not(:disabled) {
  color: #2563eb;
  background: #eaf1ff;
}

.mini-btn:disabled {
  color: #c3c8d1;
  cursor: not-allowed;
}

/* ---------- 提交列表 ---------- */
.commit-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 4px 0 8px;
}

/* 扫描进度条：一条来回扫的细光带 + 一行会自己跳秒的文案 */
.scan-strip {
  position: sticky;
  top: 0;
  z-index: 5;
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 8px 18px 9px;
  background: #f7f9ff;
  border-bottom: 1px solid #e9eefb;
}

.scan-bar {
  position: relative;
  flex: none;
  width: 46px;
  height: 3px;
  overflow: hidden;
  background: #dde6fb;
  border-radius: 3px;
}

.scan-bar i {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  width: 40%;
  background: #6f9bf0;
  border-radius: 3px;
  animation: scan-sweep 1.05s ease-in-out infinite;
}

@keyframes scan-sweep {
  0% {
    transform: translateX(-100%);
  }

  100% {
    transform: translateX(250%);
  }
}

.scan-text {
  min-width: 0;
  overflow: hidden;
  font-size: 12.5px;
  color: #6b7688;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 首次扫描、列表还空着时的居中等待态 */
.scan-empty {
  display: flex;
  flex-direction: column;
  gap: 14px;
  align-items: center;
  padding: 64px 0 40px;
}

.scan-empty p {
  margin: 0;
  font-size: 12.5px;
  color: #a3aebd;
  letter-spacing: 0.3px;
}

/* 标题右侧的「最近扫描」概要，窗口变窄时先省略它 */
.scan-meta {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  font-size: 12px;
  font-weight: 400;
  color: #a8b0bd;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.day-group + .day-group {
  margin-top: 4px;
}

.day-head {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 7px 18px;
  background: #fafbfd;
  border-top: 1px solid #f1f3f7;
  border-bottom: 1px solid #f1f3f7;
}

.day-date {
  font-size: 13px;
  font-weight: 600;
  color: #303744;
}

.day-week {
  font-size: 12px;
  color: #8a93a2;
}

.day-count {
  padding: 1px 8px;
  font-size: 11.5px;
  color: #2563eb;
  background: #eef4ff;
  border-radius: 999px;
}

.check-wrap {
  display: inline-flex;
  align-items: center;
}

.commit-row {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 8px 18px;
  cursor: pointer;
  border-bottom: 1px solid #f7f8fa;
  transition: background 0.12s;
}

.commit-row:hover {
  background: #f8fafd;
}

.commit-row.checked {
  background: #f5f9ff;
}

.c-time {
  flex: none;
  width: 40px;
  font-family: Consolas, Monaco, monospace;
  font-size: 12px;
  color: #8a93a2;
}

.c-project {
  flex: none;
  max-width: 130px;
  overflow: hidden;
  font-size: 12.5px;
  color: #2563eb;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.c-branch {
  flex: none;
  max-width: 150px;
  padding: 1px 8px;
  overflow: hidden;
  font-size: 11.5px;
  color: #4b5563;
  text-overflow: ellipsis;
  white-space: nowrap;
  background: #f2f4f8;
  border-radius: 999px;
}

.c-branch em {
  margin-left: 3px;
  font-style: normal;
  color: #2563eb;
}

.c-branch.inferred {
  color: #92400e;
  background: #fff7ed;
}

.c-branch.empty {
  color: #c3c8d1;
  background: transparent;
}

.c-msg {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 13px;
  color: #303744;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 提交说明可点开详情弹窗 */
.c-msg.clickable {
  cursor: pointer;
  border-radius: 4px;
  transition: color 0.15s, background 0.15s;
}

.c-msg.clickable:hover {
  color: #2563eb;
  background: #f0f5ff;
}

.c-hash {
  flex: none;
  font-family: Consolas, Monaco, monospace;
  font-size: 11.5px;
  color: #a8b0bd;
}

.c-author {
  flex: none;
  max-width: 96px;
  overflow: hidden;
  font-size: 12.5px;
  color: #6b7280;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.more-row {
  display: flex;
  justify-content: center;
  padding: 10px 0 4px;
}

.muted {
  font-size: 12px;
  font-weight: 400;
  color: #9aa3b2;
}

/* ---------- AI 报告 ---------- */
.report-meta-line {
  flex: none;
  padding: 10px 18px 0;
  font-size: 12.5px;
  color: #8a93a2;
}

.report-meta-line b {
  color: #2563eb;
}

.report-meta-line .warn {
  color: #d97706;
}

.report-area {
  flex: 1;
  min-height: 0;
  padding: 12px 18px;
  overflow: auto;
}

.placholder {
  min-height: 120px;
}

.report-foot {
  display: flex;
  flex: none;
  gap: 4px;
  align-items: center;
  padding: 9px 18px;
  font-size: 12px;
  color: #a8b0bd;
  border-top: 1px dashed #eef0f4;
}

.foot-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  margin-right: 4px;
  font-size: 14px;
  color: #8a93a2;
  cursor: pointer;
  background: #f2f4f8;
  border: none;
  border-radius: 6px;
  transition: all 0.15s;
}

.foot-close:hover {
  color: #2563eb;
  background: #eaf1ff;
}

/* 流式进行中的状态行：一个小呼吸点 + 已生成字数 */
.report-meta-line.streaming {
  display: flex;
  gap: 7px;
  align-items: center;
  color: #2563eb;
}

.dot-flow {
  flex: none;
  width: 7px;
  height: 7px;
  background: #2563eb;
  border-radius: 50%;
  animation: dot-pulse 1s ease-in-out infinite;
}

@keyframes dot-pulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }

  50% {
    opacity: 0.3;
    transform: scale(0.65);
  }
}

/* ---------- 报告编辑弹窗 ---------- */
.edit-dialog {
  max-width: 94vw;
}

.edit-body {
  display: flex;
  gap: 14px;
  /* 固定高度，左右两栏各自内部滚动，弹窗本身不出现滚动条 */
  height: min(64vh, 620px);
}

.edit-pane {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: 12px 14px;
  overflow: hidden;
  background: linear-gradient(#fbfcfe, #f7f9fc);
  border: 1px solid #edf0f5;
  border-radius: 12px;
}

.pane-head {
  display: flex;
  flex: none;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  padding-bottom: 9px;
  margin-bottom: 9px;
  font-size: 13px;
  font-weight: 600;
  color: #1f2937;
  border-bottom: 1px solid #eef1f6;
}

.pane-tip {
  font-size: 11.5px;
  font-weight: 400;
  color: #a8b0bd;
}

.edit-textarea {
  flex: 1;
  min-height: 0;
}

.edit-textarea :deep(.el-textarea__inner) {
  height: 100%;
  font-family: Consolas, Monaco, 'Microsoft YaHei', monospace;
  font-size: 13px;
  line-height: 1.8;
  border-radius: 8px;
}

.chat-list {
  flex: 1;
  min-height: 0;
  padding: 4px 2px 4px 0;
  overflow: auto;
}

.chat-empty {
  padding: 14px 15px;
  font-size: 12.5px;
  line-height: 1.9;
  color: #8a93a2;
  background: #fff;
  border: 1px dashed #e2e7f0;
  border-radius: 12px;
}

.chat-item {
  display: flex;
  gap: 9px;
  align-items: flex-start;
  margin-bottom: 14px;
}

/* 会话气泡左右分列：小优靠左，我的输入靠右（头像在最外侧） */
.chat-item.user {
  flex-direction: row-reverse;
}

/* 头像做成圆形：小优用青绿渐变 + 轻投影，我方浅灰蓝，比方块徽标柔和 */
.chat-role {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  font-size: 12px;
  font-weight: 600;
  color: #5a6474;
  user-select: none;
  background: #e8edf5;
  border-radius: 50%;
}

.chat-role.assistant {
  color: #fff;
  background: linear-gradient(135deg, #34d399, #0d9f9f);
  box-shadow: 0 2px 6px rgb(13 159 159 / 24%);
}

.chat-bubble {
  max-width: 76%;
  padding: 9px 13px;
  font-size: 12.5px;
  line-height: 1.8;
  color: #303744;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  background: #fff;
  border: 1px solid #e9edf4;
  border-radius: 12px;
  box-shadow: 0 1px 2px rgb(16 24 40 / 4%);
}

/* 气泡挨着头像的那个角收一收，更像聊天 */
.chat-item.assistant .chat-bubble {
  border-top-left-radius: 4px;
}

.chat-item.user .chat-bubble {
  color: #fff;
  background: linear-gradient(135deg, #4f83f7, #2563eb);
  border-color: transparent;
  border-top-right-radius: 4px;
}

.chat-bubble.typing {
  display: flex;
  gap: 8px;
  align-items: center;
  color: #0d9f9f;
}

.typing-text {
  font-size: 12.5px;
}

.typing-dots {
  display: inline-flex;
  gap: 3px;
}

.typing-dots i {
  width: 4px;
  height: 4px;
  background: currentcolor;
  border-radius: 50%;
  animation: typing-bounce 1.1s ease-in-out infinite;
}

.typing-dots i:nth-child(2) {
  animation-delay: 0.14s;
}

.typing-dots i:nth-child(3) {
  animation-delay: 0.28s;
}

@keyframes typing-bounce {
  0%,
  70%,
  100% {
    opacity: 0.35;
    transform: translateY(0);
  }

  35% {
    opacity: 1;
    transform: translateY(-3px);
  }
}

.chat-input {
  flex: none;
  padding-top: 8px;
}

.chat-toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 8px;
}

/* ---------- 弹窗通用：标题收敛一点，别顶着 18px 大字 ---------- */
.edit-dialog :deep(.el-dialog__title),
.commit-dialog :deep(.el-dialog__title) {
  font-size: 15px;
  font-weight: 600;
  color: #1f2937;
}

/* ---------- 提交详情弹窗 ---------- */
.commit-detail {
  height: 400px;
  padding-right: 2px;
  overflow: auto;
}

.cd-row {
  display: flex;
  gap: 12px;
  align-items: center;
  padding: 9px 12px;
  font-size: 12.5px;
  background: #fafbfd;
  border: 1px solid #eef0f4;
  border-radius: 9px;
}

.cd-row + .cd-row {
  margin-top: 7px;
}

.cd-label {
  flex: none;
  width: 56px;
  color: #8a93a2;
}

.cd-value {
  flex: 1;
  min-width: 0;
  color: #303744;
  overflow-wrap: anywhere;
}

.cd-value code {
  padding: 2px 7px;
  font-family: Consolas, Monaco, monospace;
  font-size: 12px;
  color: #475069;
  background: #f1f4f9;
  border-radius: 5px;
}

.cd-extra {
  margin-left: 4px;
  font-size: 11px;
  font-style: normal;
  color: #94a0b3;
}

.cd-mail {
  margin-left: 4px;
  font-size: 11.5px;
  font-style: normal;
  color: #a8b0bd;
}

.cd-empty {
  color: #c3c8d1;
}

.cd-hash {
  display: flex;
  gap: 6px;
  align-items: center;
}

.cd-copy {
  flex: none;
  color: #a8b0bd;
  cursor: pointer;
  transition: color 0.15s;
}

.cd-copy:hover {
  color: #2563eb;
}

.cd-content {
  align-items: flex-start;
  background: #fff;
}

.cd-msg {
  flex: 1;
  min-width: 0;
  max-height: 148px;
  padding: 9px 11px;
  overflow: auto;
  font-size: 12.5px;
  line-height: 1.85;
  color: #303744;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: #fafbfd;
  border: 1px solid #eef0f4;
  border-radius: 8px;
}
</style>

<!-- el-dialog 会被 teleport 到 body，scoped 选择器够不着它的标题栏，这里用全局样式收一收 -->
<style>
.edit-dialog .el-dialog__title,
.commit-dialog .el-dialog__title {
  font-size: 15px;
  font-weight: 600;
  color: #1f2937;
}

.edit-dialog .el-dialog__header,
.commit-dialog .el-dialog__header {
  padding-bottom: 12px;
}

.commit-dialog .el-dialog {
  max-width: 94vw;
}
</style>
