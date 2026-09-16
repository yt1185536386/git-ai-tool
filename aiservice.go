package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ==================== AI 分析模块 ====================
//
// 与 Electron 版 electron/main/aiService.js 保持一致：
// 把提交记录喂给 OpenAI 兼容接口生成 Markdown 报告。

// promptPreset 报告类型预设：system 定角色与输出结构，instruction 是喂给模型的指令
// （前端只需要 label，通过 GetMeta 以 PromptPresetItem 形式返回）
type promptPreset struct {
	Label       string `json:"label"`
	System      string `json:"system"`
	Instruction string `json:"instruction"`
}

// promptPresets 内置报告类型
var promptPresets = map[string]promptPreset{
	"weekly": {
		Label: "周报",
		System: "你是一名资深研发工程师，擅长把零散的 Git 提交记录整理成条理清晰的研发周报。" +
			"输出使用简体中文 Markdown，结构固定为四个二级标题：" +
			"「## 本周主要工作」「## 技术亮点」「## 风险与问题」「## 下周计划」。" +
			"内容要基于提交记录归纳，不要编造没有依据的功能；相同模块的零散提交请合并描述。",
		Instruction: "请根据以下 Git 提交记录，生成一份研发周报。",
	},
	"daily": {
		Label: "日报",
		System: "你是一名研发工程师，擅长把一天的 Git 提交记录整理成简明的工作日报。" +
			"输出使用简体中文 Markdown，结构为：「## 今日完成」「## 进行中」「## 阻塞项」。语言精炼，每条不超过两行。",
		Instruction: "请根据以下 Git 提交记录，生成一份工作日报。",
	},
	"summary": {
		Label: "变更总结",
		System: "你是一名技术负责人，擅长对代码变更做技术复盘。" +
			"输出使用简体中文 Markdown，结构为：「## 变更概览」「## 按模块拆解」「## 可能的影响面」。" +
			"重点说明变更范围与潜在回归风险。",
		Instruction: "请总结以下 Git 提交记录的关键变更点。",
	},
	"release": {
		Label: "版本发布说明",
		System: "你是一名负责发版的工程师，擅长编写面向用户和测试的发布说明。" +
			"输出使用简体中文 Markdown，结构为：「## 新功能」「## 优化改进」「## 问题修复」。" +
			"使用面向用户的表述，不要出现 commit hash、分支名等内部信息。",
		Instruction: "请根据以下 Git 提交记录，编写一份版本发布说明。",
	},
	"custom": {
		Label:       "自定义",
		System:      "",
		Instruction: "",
	},
}

// promptOrder 决定前端下拉框的展示顺序，避免 map 遍历顺序随机
var promptOrder = []string{"weekly", "daily", "summary", "release", "custom"}

// providerPresets 内置大模型服务商预设，前端通过 GetMeta 拉取
var providerPresets = []ProviderPreset{
	{Value: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1",
		Models: []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini"}},
	{Value: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com/v1",
		Models: []string{"deepseek-chat", "deepseek-reasoner"}},
	{Value: "moonshot", Label: "Moonshot 月之暗面", BaseURL: "https://api.moonshot.cn/v1",
		Models: []string{"moonshot-v1-8k", "moonshot-v1-32k"}},
	{Value: "zhipu", Label: "智谱 GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		Models: []string{"glm-4-plus", "glm-4-air"}},
	{Value: "qwen", Label: "通义千问（兼容模式）", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Models: []string{"qwen-plus", "qwen-max"}},
	{Value: "ollama", Label: "Ollama 本地模型", BaseURL: "http://127.0.0.1:11434/v1",
		Models: []string{"qwen2.5:7b", "llama3.1:8b"}},
	{Value: "custom", Label: "自定义（OpenAI 兼容）", BaseURL: "", Models: []string{}},
}

// ==================== OpenAI 兼容协议结构 ====================

// chatMessage content 可能是字符串，也可能是分片数组，因此用 RawMessage 延迟解析
type chatMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage = Usage

type chatCompletion struct {
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
}

// chatRequestBody 请求体。max_tokens 为 0 时不下发，交给服务端默认值
type chatRequestBody struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Messages    []chatMessage `json:"messages"`
}

// ==================== 工具函数 ====================

// normalizeBaseURL 规整 Base URL。
//
// 很多 OpenAI 兼容网关（one-api / new-api 之类）只在 /v1 前缀下提供接口，
// 用户按服务商主页填 `http://host:3000` 时，请求会打到网关的**首页 HTML**——
// 拿到 200 但完全不是 OpenAI 结构，最终表现为「模型返回内容为空」，极难排查。
// 因此：只有「纯 host[:port]」才补 /v1，用户已经写了路径的一律尊重。
func normalizeBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	if strings.TrimRight(parsed.Path, "/") == "" {
		parsed.Path = "/v1"
		return strings.TrimRight(parsed.String(), "/")
	}
	return trimmed
}

// extractText 从 content 字段里取正文；兼容字符串与分片数组两种形态
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asArray []json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range asArray {
		var s string
		if err := json.Unmarshal(part, &s); err == nil {
			b.WriteString(s)
			continue
		}
		var obj struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(part, &obj); err == nil {
			b.WriteString(obj.Text)
		}
	}
	return b.String()
}

// assertOpenAIResponse 校验返回体确实是 OpenAI 兼容结构。
// 命中的典型情况：Base URL 少了 /v1，网关回了首页 HTML。
func assertOpenAIResponse(body []byte, completion *chatCompletion, baseURL string) error {
	if completion != nil && completion.Choices != nil {
		return nil
	}
	looksHTML := bytes.HasPrefix(bytes.TrimSpace(body), []byte("<!doctype")) ||
		bytes.HasPrefix(bytes.TrimSpace(body), []byte("<!DOCTYPE")) ||
		bytes.HasPrefix(bytes.TrimSpace(body), []byte("<html"))
	if looksHTML {
		return fmt.Errorf("接口返回的是网页 HTML 而不是模型响应。Base URL 很可能缺少 /v1 等路径，当前用的是「%s」。", baseURL)
	}
	return fmt.Errorf("接口返回的内容不是 OpenAI 兼容格式（缺少 choices）。请检查 Base URL「%s」是否指向兼容接口。", baseURL)
}

// friendlyAIError 把底层报错翻译成用户能看懂的提示
func friendlyAIError(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "401") || strings.Contains(lower, "invalid api key") || strings.Contains(lower, "unauthorized"):
		return fmt.Errorf("鉴权失败，请检查 API Key 与 Base URL 是否匹配：%s", msg)
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return fmt.Errorf("网络请求失败，请检查 Base URL 与网络代理设置：%s", msg)
	default:
		return fmt.Errorf("AI 调用失败：%s", msg)
	}
}

// resolveAIConfig 合并配置并做必要校验（override 非空时优先，供「测试连接」使用）
func (a *App) resolveAIConfig(override *AiConfig) (AiConfig, error) {
	cfg, err := a.GetConfig()
	if err != nil {
		return AiConfig{}, err
	}
	ai := cfg.Ai
	if override != nil {
		ai = *override
	}
	ai.BaseURL = normalizeBaseURL(ai.BaseURL)
	if strings.TrimSpace(ai.APIKey) == "" {
		return ai, fmt.Errorf("尚未配置 API Key，请先到「配置」页填写大模型信息。")
	}
	if strings.TrimSpace(ai.Model) == "" {
		return ai, fmt.Errorf("尚未配置模型名称（Model），请先到「配置」页填写。")
	}
	return ai, nil
}

// aiHTTPClient 大模型生成较慢，超时给足 2 分钟
func aiHTTPClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

// postChatCompletion 发起一次 /chat/completions 调用，返回解析后的响应体
func postChatCompletion(ai AiConfig, body chatRequestBody) (*chatCompletion, error) {
	return postChatCompletionContext(context.Background(), ai, body, nil)
}

// postChatCompletionContext 发起一次 /chat/completions 调用。
// onDelta 非 nil 时以流式（stream=true）请求并逐段回调正文增量；
// 网关不支持流式（响应非 text/event-stream）时自动回退为整体解析，
// 因此调用方无需关心底层是流式还是一次性返回。
// ctx 用于中断请求（用户点「停止生成」）。
func postChatCompletionContext(ctx context.Context, ai AiConfig, body chatRequestBody, onDelta func(string)) (*chatCompletion, error) {
	if body.Temperature == 0 {
		body.Temperature = 0.3
	}
	body.Stream = onDelta != nil
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败：%w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ai.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败：%w", err)
	}
	// OpenAI 兼容协议统一使用 Bearer Token 鉴权
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ai.APIKey)

	resp, err := aiHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 流式分支：SSE 逐行解析 delta，边收边推送
	if resp.StatusCode == http.StatusOK &&
		strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return parseSSEStream(resp.Body, onDelta)
	}

	// 非流式分支（网关不支持 stream，或流式请求直接报错）
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 AI 响应失败：%w", err)
	}

	var completion chatCompletion
	// 解析失败不立刻报错：先让 assertOpenAIResponse 区分「HTML」与「非兼容格式」
	_ = json.Unmarshal(raw, &completion)

	if resp.StatusCode != http.StatusOK {
		if err := assertOpenAIResponse(raw, nil, ai.BaseURL); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("AI 接口返回 HTTP %d：%s", resp.StatusCode, string(raw))
	}
	if err := assertOpenAIResponse(raw, &completion, ai.BaseURL); err != nil {
		return nil, err
	}
	return &completion, nil
}

// parseSSEStream 解析 OpenAI 兼容的 SSE 流（data: {...} / data: [DONE]）。
// 正文增量通过 onDelta 回调；reasoning_content 收集备用（正文为空时兜底）。
func parseSSEStream(r io.Reader, onDelta func(string)) (*chatCompletion, error) {
	completion := &chatCompletion{Choices: []chatChoice{}}
	var contentBuf, reasoningBuf strings.Builder

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *chatUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Model != "" && completion.Model == "" {
			completion.Model = chunk.Model
		}
		if chunk.Usage != nil {
			completion.Usage = chunk.Usage
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				contentBuf.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			if ch.Delta.ReasoningContent != "" {
				reasoningBuf.WriteString(ch.Delta.ReasoningContent)
			}
		}
	}
	if err := sc.Err(); err != nil {
		// 用户中断（ctx 取消）会表现为读取错误，透传给上层识别
		return nil, fmt.Errorf("读取流式响应失败：%w", err)
	}

	content := contentBuf.String()
	if strings.TrimSpace(content) == "" {
		content = reasoningBuf.String()
	}
	contentJSON, _ := json.Marshal(content)
	completion.Choices = append(completion.Choices, chatChoice{Message: chatMessage{Content: contentJSON}})
	return completion, nil
}

// ==================== 上下文裁剪 ====================

// commitContext 裁剪后的提交上下文
type commitContext struct {
	Text      string
	Truncated bool
	Total     int
	Used      int
}

// buildCommitContext 把提交记录压成纯文本上下文，并按字符上限裁剪（保留最新的）
func buildCommitContext(commits []*CommitRecord, maxChars int) commitContext {
	lines := make([]string, 0, len(commits))
	for _, c := range commits {
		ref := ""
		if c.Branch != "" {
			ref = " [" + c.Branch + "]"
		}
		lines = append(lines, fmt.Sprintf("%s | %s%s | %s | %s", c.Date, c.Project, ref, c.Author, c.Message))
	}

	kept := []string{}
	size := 0
	for _, line := range lines {
		// 按字符数（rune）计，避免中文被按字节砍半
		if size+len([]rune(line))+1 > maxChars {
			break
		}
		kept = append(kept, line)
		size += len([]rune(line)) + 1
	}
	truncated := len(kept) < len(lines)
	text := strings.Join(kept, "\n")
	if truncated {
		text += fmt.Sprintf("\n\n（注：共 %d 条提交，为保证上下文长度只提供了最新的 %d 条。）", len(lines), len(kept))
	}
	return commitContext{Text: text, Truncated: truncated, Total: len(lines), Used: len(kept)}
}

// ==================== 对外能力 ====================

// AnalyzeCommits 生成报告
func (a *App) AnalyzeCommits(payload AnalyzePayload) (*AnalyzeResult, error) {
	commits := payload.Commits
	if len(commits) == 0 {
		return nil, fmt.Errorf("没有可分析的提交记录，请先扫描。")
	}

	ai, err := a.resolveAIConfig(payload.AiOverride)
	if err != nil {
		return nil, err
	}

	presetType := payload.PromptType
	if _, ok := promptPresets[presetType]; !ok {
		presetType = "weekly"
	}
	preset := promptPresets[presetType]

	maxChars := ai.MaxChars
	if maxChars <= 0 {
		maxChars = 24000
	}
	ctx := buildCommitContext(commits, maxChars)

	system := preset.System
	instruction := preset.Instruction
	if payload.PromptType == "custom" {
		system = "你是一名专业的研发助手，请严格按照用户的要求处理 Git 提交记录，输出简体中文 Markdown。"
		instruction = payload.CustomPrompt
		if strings.TrimSpace(instruction) == "" {
			instruction = "请总结以下 Git 提交记录。"
		}
	} else if strings.TrimSpace(payload.CustomPrompt) != "" {
		instruction += "\n补充要求：" + payload.CustomPrompt
	}

	projectSet := []string{}
	seenProject := map[string]bool{}
	dates := []string{}
	for _, c := range commits {
		if !seenProject[c.Project] {
			seenProject[c.Project] = true
			projectSet = append(projectSet, c.Project)
		}
		if c.Date != "" {
			dates = append(dates, c.Date)
		}
	}
	sort.Strings(dates)
	rangeText := "未知"
	if len(dates) > 0 {
		rangeText = sliceDate(dates[0]) + " ~ " + sliceDate(dates[len(dates)-1])
	}

	userContent := strings.Join([]string{
		"时间范围：" + rangeText,
		"涉及项目：" + strings.Join(projectSet, "、"),
		fmt.Sprintf("提交总数：%d", len(commits)),
		"",
		instruction,
		"",
		"```text",
		ctx.Text,
		"```",
	}, "\n")

	messages := []chatMessage{}
	if system != "" {
		systemContent, _ := json.Marshal(system)
		messages = append(messages, chatMessage{Role: "system", Content: systemContent})
	}
	userMsgContent, _ := json.Marshal(userContent)
	messages = append(messages, chatMessage{Role: "user", Content: userMsgContent})

	// 流式生成：可被 CancelAnalyze 中断，增量通过 ai:chunk 事件推给前端实时渲染
	genCtx := a.ctx
	if genCtx == nil {
		genCtx = context.Background()
	}
	httpCtx, cancel := context.WithCancel(genCtx)
	a.mu.Lock()
	a.genCancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.genCancel = nil
		a.mu.Unlock()
	}()

	emitDelta := func(delta string) {
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "ai:chunk", delta)
		}
	}

	completion, err := postChatCompletionContext(httpCtx, ai, chatRequestBody{
		Model:       ai.Model,
		Temperature: ai.Temperature,
		Messages:    messages,
	}, emitDelta)
	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			return nil, fmt.Errorf("生成已被用户停止")
		}
		return nil, friendlyAIError(err)
	}

	choice := completion.Choices[0]
	content := extractText(choice.Message.Content)
	// 部分推理模型（Kimi K3 / DeepSeek-R1 等）思考内容与正文分字段返回，
	// 极端情况下 content 为空，此时退回 reasoning_content，避免白跑一次请求
	usedReasoning := false
	if strings.TrimSpace(content) == "" && choice.Message.ReasoningContent != "" {
		content = choice.Message.ReasoningContent
		usedReasoning = true
	}
	if strings.TrimSpace(content) == "" {
		finish := firstNonEmpty(choice.FinishReason, "unknown")
		usage := "无"
		if completion.Usage != nil {
			if b, err := json.Marshal(completion.Usage); err == nil {
				usage = string(b)
			}
		}
		return nil, fmt.Errorf("模型返回内容为空（finish_reason=%s，usage=%s）。若 finish_reason 为 length，说明输出被长度限制截断，可调低 Temperature 或更换模型。", finish, usage)
	}

	model := completion.Model
	if model == "" {
		model = ai.Model
	}
	return &AnalyzeResult{
		Content: content,
		Meta: &ReportMeta{
			Provider:         ai.Provider,
			Model:            model,
			BaseURL:          ai.BaseURL,
			CommitCount:      len(commits),
			ContextTruncated: ctx.Truncated,
			ContextUsed:      ctx.Used,
			ContextTotal:     ctx.Total,
			Range:            rangeText,
			Projects:         projectSet,
			FromReasoning:    usedReasoning,
			Usage:            completion.Usage,
		},
	}, nil
}

// ==================== 报告微调 ====================

// refineHistoryLimit 会话历史的轮数上限（一对 user/assistant 记 2 条）。
// 历史里 assistant 的正文就是一份完整文档，条数太多会迅速吃满上下文，
// 因此只带最近几轮，让多轮调整能接上上下文即可。
const refineHistoryLimit = 6

// refineHistoryChars 单条历史正文的字符上限，超出只保留头部
const refineHistoryChars = 8000

// RefineReport 在已有报告的基础上按用户需求改写。
//
// 与 AnalyzeCommits 的区别：输入不是提交记录，而是「一份已经写好的文档 + 一句自然语言需求」，
// 输出要求是**完整文档**而不是解释，前端拿到后直接覆盖左侧编辑框。
// 流式增量走独立的 ai:refineChunk 事件，避免和报告生成的 ai:chunk 串台。
func (a *App) RefineReport(payload RefinePayload) (*AnalyzeResult, error) {
	report := strings.TrimSpace(payload.Report)
	if report == "" {
		return nil, fmt.Errorf("报告内容为空，无法调整。")
	}
	requirement := strings.TrimSpace(payload.Requirement)
	if requirement == "" {
		return nil, fmt.Errorf("请先输入调整需求。")
	}

	ai, err := a.resolveAIConfig(payload.AiOverride)
	if err != nil {
		return nil, err
	}

	preset, ok := promptPresets[payload.PromptType]
	if !ok {
		preset = promptPresets["weekly"]
	}
	reportName := firstNonEmpty(preset.Label, "报告")

	system := "你是一名资深研发文档编辑，负责按用户要求修改已有的《" + reportName + "》Markdown 文档。必须严格遵守：" +
		"1）只输出修改后的完整文档，不要输出任何解释、说明、前言或结语；" +
		"2）不要用 ``` 代码块把整篇文档包起来；" +
		"3）用户没有要求改动的部分保持原样，不要自行增删或改写；" +
		"4）保持简体中文 Markdown 语法与原有的标题层级结构。"

	messages := []chatMessage{}
	systemContent, _ := json.Marshal(system)
	messages = append(messages, chatMessage{Role: "system", Content: systemContent})

	// 会话历史：把之前的「需求 → 调整结果」作为真实轮次带上，
	// 这样「再精简一点」这类指代上一轮的指令才有上下文可依。
	history := payload.History
	if len(history) > refineHistoryLimit {
		history = history[len(history)-refineHistoryLimit:]
	}
	for _, turn := range history {
		if turn.Role != "user" && turn.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(turn.Content)
		if text == "" {
			continue
		}
		if turn.Role == "assistant" {
			text = truncateRunes(text, refineHistoryChars)
		}
		content, _ := json.Marshal(text)
		messages = append(messages, chatMessage{Role: turn.Role, Content: content})
	}

	userContent := strings.Join([]string{
		"当前文档如下：",
		"```markdown",
		report,
		"```",
		"",
		"请按下面的要求修改这份文档：",
		requirement,
		"",
		"直接输出修改后的完整文档。",
	}, "\n")
	userMsgContent, _ := json.Marshal(userContent)
	messages = append(messages, chatMessage{Role: "user", Content: userMsgContent})

	// 流式改写：可被 CancelRefine 中断，增量通过 ai:refineChunk 事件推给前端逐字上屏
	genCtx := a.ctx
	if genCtx == nil {
		genCtx = context.Background()
	}
	httpCtx, cancel := context.WithCancel(genCtx)
	a.mu.Lock()
	a.refineCancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.refineCancel = nil
		a.mu.Unlock()
	}()

	emitDelta := func(delta string) {
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "ai:refineChunk", delta)
		}
	}

	completion, err := postChatCompletionContext(httpCtx, ai, chatRequestBody{
		Model:       ai.Model,
		Temperature: ai.Temperature,
		Messages:    messages,
	}, emitDelta)
	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			return nil, fmt.Errorf("调整已被用户停止")
		}
		return nil, friendlyAIError(err)
	}

	choice := completion.Choices[0]
	content := extractText(choice.Message.Content)
	usedReasoning := false
	if strings.TrimSpace(content) == "" && choice.Message.ReasoningContent != "" {
		content = choice.Message.ReasoningContent
		usedReasoning = true
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("模型返回内容为空，请重试或更换模型。")
	}

	// 个别模型仍会把整篇文档塞进代码块，剥掉首尾围栏后再交给前端
	content = stripMarkdownFence(content)

	model := completion.Model
	if model == "" {
		model = ai.Model
	}
	return &AnalyzeResult{
		Content: content,
		Meta: &ReportMeta{
			Provider:      ai.Provider,
			Model:         model,
			BaseURL:       ai.BaseURL,
			FromReasoning: usedReasoning,
			Usage:         completion.Usage,
		},
	}, nil
}

// stripMarkdownFence 去掉整篇文档外层的 ``` 围栏（模型偶尔不听话时兜底）
func stripMarkdownFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return s
	}
	closing := strings.LastIndex(trimmed, "```")
	firstLineEnd := strings.Index(trimmed, "\n")
	// 闭合围栏之后还有内容、或只有一行（压根不是包裹式围栏）时原样返回
	if closing <= 3 || firstLineEnd < 0 || strings.TrimSpace(trimmed[closing+3:]) != "" {
		return s
	}
	return strings.TrimRight(trimmed[firstLineEnd+1:closing], "\n")
}

// TestAI 连通性自检：发一条极短的请求验证配置。
// 任何失败都返回 ok=false，不抛异常（返回值里带 baseUrl 便于排查）。
// Base URL / API Key 留空时回落到已保存配置（保存后前端不再回填明文 Key）。
func (a *App) TestAI(ai AiConfig) *TestResult {
	if strings.TrimSpace(ai.BaseURL) == "" || strings.TrimSpace(ai.APIKey) == "" {
		if cfg, err := a.GetConfig(); err == nil && cfg != nil {
			if strings.TrimSpace(ai.BaseURL) == "" {
				ai.BaseURL = cfg.Ai.BaseURL
			}
			if strings.TrimSpace(ai.APIKey) == "" {
				ai.APIKey = cfg.Ai.APIKey
			}
		}
	}
	resolved, err := a.resolveAIConfig(&ai)
	if err != nil {
		return &TestResult{OK: false, Message: err.Error()}
	}

	started := time.Now()
	pingContent, _ := json.Marshal("ping")
	completion, err := postChatCompletion(resolved, chatRequestBody{
		Model:     resolved.Model,
		MaxTokens: 64,
		Messages:  []chatMessage{{Role: "user", Content: pingContent}},
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return &TestResult{OK: false, Latency: latency, BaseURL: resolved.BaseURL, Message: err.Error()}
	}

	msg := completion.Choices[0].Message
	reply := extractText(msg.Content)
	if strings.TrimSpace(reply) == "" && msg.ReasoningContent != "" {
		reply = msg.ReasoningContent
	}
	model := completion.Model
	if model == "" {
		model = resolved.Model
	}
	return &TestResult{
		OK:      true,
		Latency: latency,
		Model:   model,
		BaseURL: resolved.BaseURL,
		Reply:   truncateRunes(reply, 60),
	}
}

// ListModels 查询某 OpenAI 兼容地址的全部模型。
// 与 chat 接口不同，模型列表一般不需要 Model 参数，因此这里单独处理，
// 不复用要求 Model 非空的 resolveAIConfig：Base URL / API Key 留空时落到配置里。
func (a *App) ListModels(ai AiConfig) *ModelListResult {
	if strings.TrimSpace(ai.BaseURL) == "" || strings.TrimSpace(ai.APIKey) == "" {
		if cfg, err := a.GetConfig(); err == nil && cfg != nil {
			if strings.TrimSpace(ai.BaseURL) == "" {
				ai.BaseURL = cfg.Ai.BaseURL
			}
			if strings.TrimSpace(ai.APIKey) == "" {
				ai.APIKey = cfg.Ai.APIKey
			}
		}
	}

	base := normalizeBaseURL(ai.BaseURL)
	if base == "" {
		return &ModelListResult{OK: false, Message: "请先填写 Base URL"}
	}
	if strings.TrimSpace(ai.APIKey) == "" {
		return &ModelListResult{OK: false, BaseURL: base, Message: "请先填写 API Key，未鉴权的网关一般会拒绝返回模型列表"}
	}

	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return &ModelListResult{OK: false, Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+ai.APIKey)
	req.Header.Set("Accept", "application/json")

	// 模型列表是小请求，超时收紧到 30s
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ModelListResult{OK: false, BaseURL: base, Message: friendlyAIError(err).Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<")) {
			return &ModelListResult{OK: false, BaseURL: base,
				Message: "接口返回的是网页 HTML 而不是模型列表，Base URL 很可能缺少 /v1 等路径。当前用「" + base + "」"}
		}
		return &ModelListResult{OK: false, BaseURL: base,
			Message: fmt.Sprintf("模型列表接口返回 HTTP %d：%s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(raw)), 200))}
	}

	ids := extractModelIDs(raw)
	if len(ids) == 0 {
		return &ModelListResult{OK: false, BaseURL: base,
			Message: "接口未返回任何模型，请确认该地址是否为 OpenAI 兼容的 /models 接口，或 Base URL 是否缺少 /v1"}
	}
	sort.Strings(ids)
	return &ModelListResult{OK: true, Models: ids, BaseURL: base}
}

// extractModelIDs 兼容两种常见 /models 返回：{"data":[{id,...}]} 与直接数组
func extractModelIDs(raw []byte) []string {
	var asObj struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &asObj); err == nil && len(asObj.Data) > 0 {
		out := make([]string, 0, len(asObj.Data))
		for _, d := range asObj.Data {
			if d.ID != "" {
				out = append(out, d.ID)
			}
		}
		return out
	}
	var arr []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil {
		out := make([]string, 0, len(arr))
		for _, d := range arr {
			if d.ID != "" {
				out = append(out, d.ID)
			}
		}
		return out
	}
	return nil
}

// ==================== 小工具 ====================

// truncateRunes 按字符截断
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// sliceDate 取 ISO 时间的前 10 位（YYYY-MM-DD）
func sliceDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
