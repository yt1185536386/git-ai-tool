package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	debug "runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ==================== 数据结构定义 ====================

// appVersion 应用版本号，展示在顶栏与配置页页脚
const appVersion = "1.1.1"

// AiConfig 大模型相关配置
type AiConfig struct {
	Provider    string  `json:"provider"`    // 模型来源: openai / deepseek / moonshot / zhipu / qwen / ollama / custom
	APIKey      string  `json:"apiKey"`      // 大模型 API Key
	BaseURL     string  `json:"baseUrl"`     // API 地址，留空则使用 Provider 对应默认地址
	Model       string  `json:"model"`       // 模型名称，如 deepseek-chat
	Temperature float64 `json:"temperature"` // 采样温度
	MaxChars    int     `json:"maxChars"`    // 提交上下文最大字符数，超出只保留最新的部分
}

// ScanConfig 扫描参数
type ScanConfig struct {
	SinceDays  int `json:"sinceDays"`  // 默认时间范围（天），0 表示不限
	MaxDepth   int `json:"maxDepth"`   // 工作区下递归查找仓库的深度上限
	MaxCommits int `json:"maxCommits"` // 单仓库提交数上限
}

// Config 应用配置，持久化到 exe 同级目录的 config.json
type Config struct {
	Ai         AiConfig     `json:"ai"`
	Workspaces []string     `json:"workspaces"` // 扫描的工作区目录（可多个）
	Scan       ScanConfig   `json:"scan"`
}

// legacyConfig 旧版配置结构（平铺字段），用于自动迁移
type legacyConfig struct {
	Provider        string   `json:"provider"`
	APIKey          string   `json:"api_key"`
	ModelName       string   `json:"model_name"`
	BaseURL         string   `json:"base_url"`
	WorkspacePaths  []string `json:"workspace_paths"`
}

// ProviderPreset 大模型服务商预设
type ProviderPreset struct {
	Value   string   `json:"value"`
	Label   string   `json:"label"`
	BaseURL string   `json:"baseUrl"`
	Models  []string `json:"models"`
}

// PromptPresetItem 报告类型预设（前端下拉框用）
type PromptPresetItem struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Usage OpenAI 协议里的 token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Versions 运行环境版本信息，展示在顶栏与配置页页脚
type Versions struct {
	App   string `json:"app"`
	Wails string `json:"wails"`
	Go    string `json:"go"`
	OS    string `json:"os"`
}

// Meta 应用元信息，对应 Electron 版的 get-meta
type Meta struct {
	Providers     []ProviderPreset   `json:"providers"`
	PromptPresets []PromptPresetItem `json:"promptPresets"`
	ConfigPath    string             `json:"configPath"`
	Versions      Versions           `json:"versions"`
}

// CommitRecord 单条 Git 提交记录
type CommitRecord struct {
	Hash           string `json:"hash"`           // 完整 hash
	ShortHash      string `json:"shortHash"`      // 短 hash
	Date           string `json:"date"`           // 提交时间（ISO-8601 strict，带时区）
	Author         string `json:"author"`         // 作者名
	Email          string `json:"email"`          // 作者邮箱
	Message        string `json:"message"`        // 提交说明（首行）
	Body           string `json:"body"`           // 提交说明正文
	Refs           string `json:"refs"`           // 原始 ref 串
	Branch         string `json:"branch"`         // 展示用分支名
	BranchInferred bool   `json:"branchInferred"` // 分支名是否为归属推断（非 ref 直接指向）
	BranchCount    int    `json:"branchCount"`    // 该提交同时存在于几个分支
	Project        string `json:"project"`        // 项目名（仓库目录名）
	Repo           string `json:"repo"`           // 仓库绝对路径

	// ts 解析好的提交时间，只用于排序比较，不参与序列化
	ts time.Time
}

// RepoInfo 单个仓库的元信息
type RepoInfo struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	CurrentBranch string   `json:"currentBranch"`
	Remote        string   `json:"remote"`
	Remotes       []string `json:"remotes"`
	CommitCount   int      `json:"commitCount"`
	Error         string   `json:"error"`
}

// ScanPayload 扫描入参（前端传入的仓库、时间范围等会覆盖配置里的默认值）
type ScanPayload struct {
	// SinceDays 用指针是为了区分「没传」和「传了 0」：
	// 0 表示不限时间范围（界面上的「全部」），nil 才回落到配置里的默认天数
	SinceDays  *int     `json:"sinceDays"`
	Since      string   `json:"since"`
	Until      string   `json:"until"`
	Workspaces []string `json:"workspaces"`
	MaxDepth   int      `json:"maxDepth"`
	MaxCommits int      `json:"maxCommits"`
}

// ScanResult 扫描结果
type ScanResult struct {
	Commits  []*CommitRecord `json:"commits"`
	Repos    []*RepoInfo     `json:"repos"`
	Warnings []string        `json:"warnings"`
	// Seq 本次扫描的请求序号。前端拿它跟进度事件里的 seq 对齐，
	// 丢弃「上一次扫描迟到的返回」，避免快速切时间范围时结果闪回旧数据
	Seq int64 `json:"seq"`
	// ElapsedMs 本次扫描实际耗时（毫秒），给界面显示用
	ElapsedMs int64 `json:"elapsedMs"`
	// Cached 为 true 表示命中了短时缓存，没有真正重新扫描
	Cached bool `json:"cached"`
	// Superseded 为 true 表示这次扫描在途中被更新的请求顶掉了，前端应直接忽略
	Superseded bool `json:"superseded"`
}

// AnalyzePayload AI 分析入参
type AnalyzePayload struct {
	Commits      []*CommitRecord `json:"commits"`
	PromptType   string          `json:"promptType"`
	CustomPrompt string          `json:"customPrompt"`
	AiOverride   *AiConfig       `json:"aiOverride"` // 「测试连接」用临时配置覆盖
}

// ChatTurn 编辑弹窗里的一轮会话（用户需求 / 模型上一轮的调整结果）
type ChatTurn struct {
	Role    string `json:"role"`    // user / assistant
	Content string `json:"content"` // 用户需求，或模型返回的完整文档
}

// RefinePayload 报告微调入参：
// 把「当前文档 + 用户本次需求 + 之前的会话」一起交给模型改写
type RefinePayload struct {
	Report      string     `json:"report"`      // 当前报告原文（以编辑框里的内容为准）
	Requirement string     `json:"requirement"` // 本次调整需求
	History     []ChatTurn `json:"history"`     // 会话历史，用于多轮连续调整
	PromptType  string     `json:"promptType"`  // 报告类型，决定 system 提示词里的称谓
	AiOverride  *AiConfig  `json:"aiOverride"`  // 弹窗内切换模型时的临时配置覆盖
}

// ReportMeta 报告生成过程的元信息
type ReportMeta struct {
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	BaseURL          string   `json:"baseUrl"`
	CommitCount      int      `json:"commitCount"`
	ContextTruncated bool     `json:"contextTruncated"`
	ContextUsed      int      `json:"contextUsed"`
	ContextTotal     int      `json:"contextTotal"`
	Range            string   `json:"range"`
	Projects         []string `json:"projects"`
	FromReasoning    bool     `json:"fromReasoning"`
	Usage            *Usage   `json:"usage"`
}

// AnalyzeResult AI 分析结果
type AnalyzeResult struct {
	Content string      `json:"content"`
	Meta    *ReportMeta `json:"meta"`
}

// TestResult 连通性测试结果
type TestResult struct {
	OK      bool   `json:"ok"`
	Latency int64  `json:"latency"`
	Model   string `json:"model"`
	BaseURL string `json:"baseUrl"`
	Reply   string `json:"reply"`
	Message string `json:"message"`
}

// SaveResult 保存结果
type SaveResult struct {
	OK       bool   `json:"ok"`
	Canceled bool   `json:"canceled"`
	Path     string `json:"path"`
}

// ModelListResult 模型列表查询结果（查询某 OpenAI 兼容地址下的全部模型）
type ModelListResult struct {
	OK      bool     `json:"ok"`
	Models  []string `json:"models"`
	Message string   `json:"message"`
	BaseURL string   `json:"baseUrl"`
}

// ==================== 应用主体 ====================

// App 应用主体，公开方法通过 Wails 绑定暴露给前端调用
type App struct {
	ctx          context.Context
	mu           sync.RWMutex       // 保护配置文件并发读写
	genCancel    context.CancelFunc // 当前报告生成任务的取消函数；nil 表示无进行中的任务
	refineCancel context.CancelFunc // 当前报告微调任务的取消函数；与 genCancel 相互独立，互不影响

	// 扫描调度：同一时刻只允许一次扫描在跑，新的扫描会顶掉旧的
	scanMu     sync.Mutex
	scanSeq    int64
	scanCancel context.CancelFunc
	scanCache  *scanCache // 短时结果缓存，来回切时间范围时直接用
}

// NewApp 创建应用实例
func NewApp() *App {
	return &App{scanCache: newScanCache()}
}

// startup Wails 生命周期钩子：窗口创建前注入上下文
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// ==================== 配置路径 ====================

// defaultConfig 首次启动使用的默认配置
func defaultConfig() Config {
	return Config{
		Ai: AiConfig{
			Provider:    "deepseek",
			BaseURL:     "https://api.deepseek.com/v1",
			Model:       "deepseek-chat",
			Temperature: 0.3,
			MaxChars:    24000,
		},
		Workspaces: []string{},
		Scan:       ScanConfig{SinceDays: 7, MaxDepth: 3, MaxCommits: 800},
	}
}

// configFilePath 配置文件路径：固定放在 exe 同级目录，绿色版直接连 exe 一起拷贝即可
func configFilePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(filepath.Dir(exe), "config.json")
}

// configPath 当前生效的配置文件路径，供界面展示与「打开配置文件位置」使用
func (a *App) configPath() string {
	return configFilePath()
}

// readConfigFile 读取配置文件原始内容；文件不存在时返回 nil 表示首次使用。
// 统一剥离 UTF-8 BOM：部分编辑器（如 Windows 记事本）保存时会带 BOM，
// 而 Go 的 JSON 解析不接受 BOM 前缀，会导致 "invalid character 'ï'" 解析失败。
func (a *App) readConfigFile() ([]byte, error) {
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 config.json 失败：%w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	return data, nil
}

// ==================== 配置读写 ====================

// normalizeConfig 修正非法/缺省值，避免扫描时出现 0 深度、负天数等越界参数
func normalizeConfig(cfg *Config) {
	if cfg.Workspaces == nil {
		cfg.Workspaces = []string{}
	}
	for i := range cfg.Workspaces {
		cfg.Workspaces[i] = strings.TrimSpace(cfg.Workspaces[i])
	}
	if cfg.Ai.Provider == "" {
		cfg.Ai.Provider = "deepseek"
	}
	if cfg.Ai.MaxChars <= 0 {
		cfg.Ai.MaxChars = 24000
	}
	if cfg.Scan.SinceDays < 0 {
		cfg.Scan.SinceDays = 0
	}
	if cfg.Scan.MaxDepth <= 0 {
		cfg.Scan.MaxDepth = 3
	}
	if cfg.Scan.MaxCommits <= 0 {
		cfg.Scan.MaxCommits = 800
	}
}

// loadConfigLocked 读取配置，调用方需自行持有锁
func (a *App) loadConfigLocked() (*Config, error) {
	cfg := defaultConfig()
	data, err := a.readConfigFile()
	if err != nil {
		return &cfg, err
	}
	if len(data) == 0 {
		return &cfg, nil
	}

	// 探测是否旧版结构：没有 ai 字段即视为旧版，迁移后返回
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return &cfg, fmt.Errorf("config.json 格式非法：%w", err)
	}
	if _, hasAi := probe["ai"]; !hasAi {
		var legacy legacyConfig
		if err := json.Unmarshal(data, &legacy); err != nil {
			return &cfg, fmt.Errorf("config.json 格式非法：%w", err)
		}
		if legacy.Provider != "" {
			cfg.Ai.Provider = legacy.Provider
		}
		cfg.Ai.APIKey = legacy.APIKey
		if legacy.ModelName != "" {
			cfg.Ai.Model = legacy.ModelName
		}
		cfg.Ai.BaseURL = legacy.BaseURL
		cfg.Workspaces = legacy.WorkspacePaths
		normalizeConfig(&cfg)
		return &cfg, nil
	}

	// 新结构：直接反序列化到默认值上，等价于按字段深合并（缺省字段保留默认值）
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &cfg, fmt.Errorf("config.json 格式非法：%w", err)
	}
	normalizeConfig(&cfg)
	return &cfg, nil
}

// GetConfig 读取当前配置
func (a *App) GetConfig() (*Config, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.loadConfigLocked()
}

// SaveConfig 全量保存配置。
// 前端每次都会提交完整的 ai / workspaces / scan 三块，因此这里按全量写入处理。
// API Key 在保存后不再回显明文（前端提交空值表示"保持不变"），避免界面泄露密钥。
func (a *App) SaveConfig(cfg Config) (*Config, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 前端提交的 apiKey 为空时保留配置文件里的旧值
	if strings.TrimSpace(cfg.Ai.APIKey) == "" {
		if old, err := a.loadConfigLocked(); err == nil {
			cfg.Ai.APIKey = old.Ai.APIKey
		}
	}

	normalizeConfig(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化配置失败：%w", err)
	}

	target := configFilePath()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("创建配置目录失败：%w", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return nil, fmt.Errorf("写入 config.json 失败：%w", err)
	}
	return &cfg, nil
}

// CheckConfig 校验配置完整性：至少一个工作区、API Key、模型名称缺一不可
func (a *App) CheckConfig() bool {
	cfg, err := a.GetConfig()
	if err != nil || cfg == nil {
		return false
	}
	return len(cfg.Workspaces) > 0 &&
		strings.TrimSpace(cfg.Ai.APIKey) != "" &&
		strings.TrimSpace(cfg.Ai.Model) != ""
}

// CancelAnalyze 停止正在进行的报告生成；无进行中的任务时为空操作
func (a *App) CancelAnalyze() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.genCancel != nil {
		a.genCancel()
	}
}

// CancelRefine 停止正在进行中的报告微调；无进行中的任务时为空操作
func (a *App) CancelRefine() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refineCancel != nil {
		a.refineCancel()
	}
}

// ==================== 元信息 ====================

// wailsVersion 从构建信息里取 Wails 版本，取不到时返回 unknown
func wailsVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if strings.HasPrefix(dep.Path, "github.com/wailsapp/wails") {
			return dep.Version
		}
	}
	return "unknown"
}

// GetMeta 返回服务商预设、提示词预设、配置文件位置与版本信息
func (a *App) GetMeta() *Meta {
	presets := make([]PromptPresetItem, 0, len(promptOrder))
	for _, key := range promptOrder {
		preset, ok := promptPresets[key]
		if !ok {
			continue
		}
		presets = append(presets, PromptPresetItem{Value: key, Label: preset.Label})
	}
	return &Meta{
		Providers:     providerPresets,
		PromptPresets: presets,
		ConfigPath:    a.configPath(),
		Versions: Versions{
			App:   appVersion,
			Wails: wailsVersion(),
			Go:    runtime.Version(),
			OS:    runtime.GOOS + "/" + runtime.GOARCH,
		},
	}
}

// ==================== 扫描 ====================

// scanCacheTTL 扫描结果短时缓存的有效期。
// 界面上来回切「近 7 天 / 近 30 天」这类操作很常见，命中缓存能做到瞬时响应；
// 时间短到不至于让人看到过期数据。
const scanCacheTTL = 30 * time.Second

// scanCache 按入参签名缓存最近一次扫描结果（只存一份，够用且不占内存）
type scanCache struct {
	mu     sync.Mutex
	key    string
	result *ScanResult
	at     time.Time
}

func newScanCache() *scanCache { return &scanCache{} }

func (c *scanCache) get(key string) *ScanResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.result == nil || c.key != key || time.Since(c.at) > scanCacheTTL {
		return nil
	}
	return c.result
}

func (c *scanCache) put(key string, result *ScanResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key = key
	c.result = result
	c.at = time.Now()
}

// ScanGit 扫描配置的工作区目录下的 git 仓库，聚合提交记录。
// 未传入的扫描参数（工作区、时间范围、深度等）一律回落到配置里的默认值。
func (a *App) ScanGit(payload ScanPayload) (*ScanResult, error) {
	cfg, err := a.GetConfig()
	if err != nil {
		return nil, err
	}
	if len(payload.Workspaces) == 0 {
		payload.Workspaces = cfg.Workspaces
	}
	if payload.SinceDays == nil && payload.Since == "" {
		sinceDays := cfg.Scan.SinceDays
		payload.SinceDays = &sinceDays
	}
	if payload.MaxDepth <= 0 {
		payload.MaxDepth = cfg.Scan.MaxDepth
	}
	if payload.MaxCommits <= 0 {
		payload.MaxCommits = cfg.Scan.MaxCommits
	}

	cacheKey := payload.cacheKey()
	if cached := a.scanCache.get(cacheKey); cached != nil {
		hit := *cached
		hit.Cached = true
		hit.Superseded = false
		return &hit, nil
	}

	// 新的扫描顶掉还没跑完的旧扫描：用户连点几个时间范围时不会把旧活儿干完
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	a.scanMu.Lock()
	if a.scanCancel != nil {
		a.scanCancel()
	}
	ctx, cancel := context.WithCancel(parent)
	a.scanCancel = cancel
	a.scanSeq++
	seq := a.scanSeq
	a.scanMu.Unlock()
	defer cancel()

	tracker := newProgressTracker(seq, a.emitScanProgress)
	result := scanWorkspacesCtx(ctx, payload.Workspaces, payload, tracker)

	// 收尾：只有自己还是当前扫描时才清掉取消函数
	a.scanMu.Lock()
	if a.scanSeq == seq {
		a.scanCancel = nil
	}
	a.scanMu.Unlock()

	if ctx.Err() != nil {
		// 被更新的请求取代了。返回一个空结果并打上标记，前端静默丢弃，不弹错误
		return &ScanResult{
			Commits:    []*CommitRecord{},
			Repos:      []*RepoInfo{},
			Warnings:   []string{},
			Seq:        seq,
			Superseded: true,
		}, nil
	}

	result.Seq = seq
	a.scanCache.put(cacheKey, result)
	return result, nil
}

// cacheKey 扫描入参签名：参数一样就用同一份缓存
func (p ScanPayload) cacheKey() string {
	sinceDays := "-"
	if p.SinceDays != nil {
		sinceDays = strconv.Itoa(*p.SinceDays)
	}
	return strings.Join([]string{
		strings.Join(p.Workspaces, "|"),
		sinceDays,
		p.Since,
		p.Until,
		strconv.Itoa(p.MaxDepth),
		strconv.Itoa(p.MaxCommits),
	}, "\x1f")
}

// emitScanProgress 把扫描进度转发给前端（scan:progress 事件）
func (a *App) emitScanProgress(p ScanProgress) {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, "scan:progress", p)
}
