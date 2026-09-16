package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==================== 本地 Git 数据源模块 ====================
//
// 扫描配置的工作区目录下的所有 git 仓库，通过本地 `git log` 读取提交记录。
//
// 性能设计的前提（本机实测）：每拉起一次 git 进程要 300~500ms（杀软 / EDR 会扫描
// 每个新进程的映像），而 git 在仓库里真正干活只要几十毫秒。也就是说扫描耗时几乎
// 正比于「git 进程个数」，优化方向只有两条：少起进程、并起进程。
//
//	旧实现：每仓库 2(inspect) + 1(branch -a) + N(逐分支 log) + 1(log)
//	        29 个分支 → 33 个进程 → 十几秒，其中逐分支 log 占九成
//	新实现：每仓库 2(inspect) + 1(for-each-ref) + 1(提交图) + 1(log) = 5 个进程，
//	        且这几路工作并发执行、仓库之间也并发 → 一秒出头
//
// 分支归属不再逐分支 log，而是取一次完整提交图 + 在内存里沿祖先方向传播 bitmask：
// `rev-list --date-order` 保证「父提交一定出现在它的所有子提交之后」，于是按输出
// 顺序单遍扫描，把各分支顶端的 bitmask 一路累加到祖先即可，结果与逐分支遍历等价。

const (
	// maxScanBranches 参与归属判定的分支上限。超出时按分支顶端提交时间保留最新的
	// 若干个（旧实现是直接放弃全部归属信息，损失更大）
	maxScanBranches = 30
	// maxGraphCommits 提交图单次遍历的提交数上限，避免超大仓库在内存里堆太多状态。
	// 提交图按时间倒序输出，窗口内的提交在输出最前面，截断只会影响很老的提交。
	maxGraphCommits = 200000
	// maxConcurrentGit 全局同时运行的 git 进程数上限。并发实测能拿到约 2 倍收益
	// （4 个并发 745ms vs 串行 1501ms），但并发过多会互相拖慢，取个甜点值。
	maxConcurrentGit = 6
	// maxConcurrentRepos 同时扫描的仓库数上限
	maxConcurrentRepos = 3
)

const (
	// gitLogFieldSep 字段分隔符（ASCII 单元分隔符，日志内容几乎不会包含）
	gitLogFieldSep = "\x1f"
	// gitRecordSep 提交之间的记录分隔符（ASCII 记录分隔符）。
	// 提交正文本身可以带换行，按行切会把一条提交切成好几行，必须用记录分隔符切。
	gitRecordSep = "\x1e"
	// gitLogFormat git log 占位符格式：hash / 短 hash / 作者 / 邮箱 / 时间 / 说明 / 正文 / ref
	gitLogFormat = "%H\x1f%h\x1f%an\x1f%ae\x1f%ad\x1f%s\x1f%b\x1f%D"
	// gitBranchFormat for-each-ref 占位符格式：完整 ref 名 / 顶端 hash / 符号引用 / 顶端提交时间
	gitBranchFormat = "%(refname)\x1f%(objectname)\x1f%(symref)\x1f%(committerdate:iso-strict)"
)

// HEAD git log %D 里出现的当前分支检出标记
const HEAD = "HEAD"

// ScanProgress 扫描进度，通过 scan:progress 事件实时推给前端
type ScanProgress struct {
	Seq        int64  `json:"seq"`        // 请求序号，前端据此丢弃过期进度
	Phase      string `json:"phase"`      // discover（找仓库）/ scan（扫仓库）/ done
	ReposTotal int    `json:"reposTotal"` // 发现的仓库总数
	ReposDone  int    `json:"reposDone"`  // 已扫完的仓库数
	Current    string `json:"current"`    // 当前/刚完成的仓库名
	Commits    int    `json:"commits"`    // 已累计提交数
	ElapsedMs  int64  `json:"elapsedMs"`  // 已耗时
}

// scanWindow 解析后的扫描时间窗
type scanWindow struct {
	sinceArg  string    // 原样传给 git 的 --since
	untilArg  string    // 原样传给 git 的 --until
	sinceTime time.Time // 解析后的下界，用于提前剔除不可能命中的分支
}

// branchRef 一个可参与归属判定的分支
type branchRef struct {
	Name string    // 展示用短名（本地分支去掉 refs/heads/，远端分支去掉 refs/remotes/<remote>/）
	Hash string    // 分支顶端提交
	Tip  time.Time // 顶端提交时间
}

// ==================== 并发闸门 ====================

var (
	gitSemOnce sync.Once
	gitSem     chan struct{}
)

// gitProcessGate 全局 git 进程闸门：限制同时在跑的 git.exe 数量
func gitProcessGate() chan struct{} {
	gitSemOnce.Do(func() {
		n := runtime.NumCPU()
		if n < 3 {
			n = 3
		}
		if n > maxConcurrentGit {
			n = maxConcurrentGit
		}
		gitSem = make(chan struct{}, n)
	})
	return gitSem
}

// runGitCtx 在指定目录执行 git 命令，返回 stdout；出错时返回空串。
// 所有 git 调用都要走这里，以便统一限流和响应取消。
func runGitCtx(ctx context.Context, dir string, args ...string) string {
	if ctx == nil {
		ctx = context.Background()
	}
	gate := gitProcessGate()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ""
	}
	defer func() { <-gate }()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	hideChildConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// ==================== 扫描入口 ====================

// scanWorkspaces 扫描多个工作区目录下的 git 仓库，聚合提交记录。
// 找不到任何工作区/仓库时返回带 warning 的空结果。
func scanWorkspaces(workspaces []string, payload ScanPayload) *ScanResult {
	return scanWorkspacesCtx(context.Background(), workspaces, payload, nil)
}

// scanWorkspacesCtx 是带取消与进度上报的扫描实现。
func scanWorkspacesCtx(
	ctx context.Context,
	workspaces []string,
	payload ScanPayload,
	tracker *progressTracker,
) *ScanResult {
	started := time.Now()
	result := &ScanResult{Commits: []*CommitRecord{}, Repos: []*RepoInfo{}, Warnings: []string{}}
	if len(workspaces) == 0 {
		result.Warnings = append(result.Warnings, "尚未配置扫描工作区，请先到「配置」页添加。")
		return result
	}

	tracker.report("discover", "", true)
	repos := discoverRepos(workspaces, payload.MaxDepth)
	if len(repos) == 0 {
		result.Warnings = append(result.Warnings, "在配置的工作区中未发现 git 仓库。")
		return result
	}
	result.Repos = make([]*RepoInfo, len(repos))
	tracker.setReposTotal(len(repos))

	window := resolveScanWindow(payload)
	outcomes := make([]repoOutcome, len(repos))

	// 仓库之间并发扫描；索引写回，保证结果顺序与发现顺序一致
	pool := len(repos)
	if pool > maxConcurrentRepos {
		pool = maxConcurrentRepos
	}
	tasks := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < pool; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range tasks {
				outcomes[i] = scanOneRepo(ctx, repos[i], payload, window, tracker)
			}
		}()
	}
	for i := range repos {
		if ctx.Err() != nil {
			break
		}
		tasks <- i
	}
	close(tasks)
	wg.Wait()

	all := []*CommitRecord{}
	for i, o := range outcomes {
		if o.meta == nil {
			continue
		}
		result.Repos[i] = o.meta
		if o.meta.Error != "" {
			continue
		}
		all = append(all, o.commits...)
	}

	// 按提交时间倒序（最新在前）。排序键在解析时就算好了，避免比较函数里反复解析时间串
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].ts.After(all[j].ts)
	})
	result.Commits = all
	tracker.report("done", "", true)
	result.ElapsedMs = time.Since(started).Milliseconds()
	return result
}

// repoOutcome 单个仓库的扫描产物
type repoOutcome struct {
	meta    *RepoInfo
	commits []*CommitRecord
}

// scanOneRepo 扫描单个仓库。
// 四类工作互不依赖（仓库元信息 / 分支列表 / 提交图 / 提交记录），全部并发起，
// 总耗时约等于最慢那一路；git 进程数由全局闸门统一限制。
func scanOneRepo(
	ctx context.Context,
	dir string,
	payload ScanPayload,
	window scanWindow,
	tracker *progressTracker,
) repoOutcome {
	var (
		meta     *RepoInfo
		records  []*CommitRecord
		refs     []branchRef
		graphOut string
	)

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		meta = inspectRepo(ctx, dir)
	}()
	go func() {
		defer wg.Done()
		refs = listBranchRefs(ctx, dir, window.sinceTime)
	}()
	go func() {
		defer wg.Done()
		graphOut = fetchCommitGraph(ctx, dir, window)
	}()
	go func() {
		defer wg.Done()
		records = fetchRepoLog(ctx, dir, payload, window)
	}()
	wg.Wait()

	if meta == nil {
		meta = &RepoInfo{Name: filepath.Base(dir), Path: dir}
	}
	// 归属计算要用到「分支列表」和「提交图」两份数据，等它俩都到齐再做（纯内存计算，很快）
	masks := buildBranchMasks(refs, graphOut)
	applyBranchInfo(records, refs, masks)
	meta.CommitCount = len(records)
	tracker.repoFinished(meta.Name, len(records))
	return repoOutcome{meta: meta, commits: records}
}

// resolveScanWindow 把入参里的时间范围解析成 git 参数 + 可比较的时间下界
func resolveScanWindow(payload ScanPayload) scanWindow {
	w := scanWindow{untilArg: strings.TrimSpace(payload.Until)}
	if s := strings.TrimSpace(payload.Since); s != "" {
		w.sinceArg = s
	} else if payload.SinceDays != nil && *payload.SinceDays > 0 {
		w.sinceArg = time.Now().AddDate(0, 0, -*payload.SinceDays).Format(time.RFC3339)
	}
	if w.sinceArg != "" {
		w.sinceTime = parseSinceArg(w.sinceArg)
	}
	return w
}

// parseSinceArg 解析 --since 的取值。界面上的自定义范围传的是 YYYY-MM-DD，
// 解析不出来就返回零值 —— 宁可多保留几个分支，也不能漏掉归属。
func parseSinceArg(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t
	}
	return time.Time{}
}

// ==================== 仓库发现 ====================

// nonSourceDirs 递归找仓库时直接跳过的目录名：里面不可能有需要统计的仓库，
// 但子目录动辄上万（node_modules），下探纯属浪费
var nonSourceDirs = map[string]bool{
	"node_modules":     true,
	"bower_components": true,
	"vendor":           true,
	"__pycache__":      true,
	"site-packages":    true,
	"venv":             true,
}

// discoverRepos 在给定工作区目录下递归查找含 .git 的仓库，MaxDepth 控制向下查找深度。
func discoverRepos(workspaces []string, maxDepth int) []string {
	if maxDepth <= 0 {
		maxDepth = 3
	}
	seen := map[string]bool{}
	repos := []string{}

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		// 当前目录本身是仓库？直接收录并返回，不再下探
		for _, e := range entries {
			if e.IsDir() && e.Name() == ".git" {
				if !seen[dir] {
					seen[dir] = true
					repos = append(repos, dir)
				}
				return
			}
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			// 符号链接指回上层会造成无限递归，跳过
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			if nonSourceDirs[strings.ToLower(e.Name())] {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}

	for _, ws := range workspaces {
		if ws = strings.TrimSpace(ws); ws == "" {
			continue
		}
		walk(ws, 1)
	}
	return repos
}

// ==================== 单个仓库的取数 ====================

// inspectRepo 获取单个仓库的元信息（分支、远程等）。
// 两次 git 调用互不依赖，并发跑。
func inspectRepo(ctx context.Context, dir string) *RepoInfo {
	info := &RepoInfo{
		Name: filepath.Base(dir),
		Path: dir,
	}
	var current, remoteOut string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		current = runGitCtx(ctx, dir, "symbolic-ref", "--short", "HEAD")
	}()
	go func() {
		defer wg.Done()
		remoteOut = runGitCtx(ctx, dir, "remote", "-v")
	}()
	wg.Wait()

	info.CurrentBranch = strings.TrimSpace(current)
	remotes := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(remoteOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		remotes = append(remotes, fields[1])
	}
	info.Remotes = remotes
	if len(remotes) > 0 {
		info.Remote = remotes[0]
	}
	return info
}

// listBranchRefs 列出参与归属判定的分支（含远端分支）。
// 一次 for-each-ref 即可拿到全部分支名与顶端提交，替代过去逐分支的多次调用。
func listBranchRefs(ctx context.Context, dir string, since time.Time) []branchRef {
	out := runGitCtx(ctx, dir, "for-each-ref", "--format="+gitBranchFormat, "refs/heads", "refs/remotes")
	if strings.TrimSpace(out) == "" {
		return nil
	}

	refs := []branchRef{}
	indexByName := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), gitLogFieldSep)
		if len(fields) < 4 {
			continue
		}
		// %(symref) 非空说明是 origin/HEAD 这类指向别的 ref 的符号引用，不参与归属
		if strings.TrimSpace(fields[2]) != "" {
			continue
		}
		name := shortBranchName(fields[0])
		if name == "" {
			continue
		}
		tip := parseGitTime(normalizeGitTime(fields[3]))
		if idx, ok := indexByName[name]; ok {
			// 同名分支（本地 + 远端）合并成一个，顶端取更新的那个
			if tip.After(refs[idx].Tip) {
				refs[idx].Tip = tip
				refs[idx].Hash = strings.TrimSpace(fields[1])
			}
			continue
		}
		indexByName[name] = len(refs)
		refs = append(refs, branchRef{
			Name: name,
			Hash: strings.TrimSpace(fields[1]),
			Tip:  tip,
		})
	}

	// 顶端早于时间窗下界的分支不可能贡献窗口内的提交，直接不参与归属计算
	if !since.IsZero() {
		kept := make([]branchRef, 0, len(refs))
		for _, r := range refs {
			if r.Tip.IsZero() || !r.Tip.Before(since) {
				kept = append(kept, r)
			}
		}
		refs = kept
	}

	// 分支过多时保留顶端最新的若干个，至少比「超限就全部放弃」有信息量
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].Tip.After(refs[j].Tip) })
	if len(refs) > maxScanBranches {
		refs = refs[:maxScanBranches]
	}
	return refs
}

// shortBranchName 把完整 ref 名转成展示用短名：
//
//	refs/heads/develop            -> develop
//	refs/heads/feature/x          -> feature/x（本地分支名里的 / 要保留）
//	refs/remotes/origin/develop   -> develop（丢掉远端名，与本地同名分支合并）
func shortBranchName(full string) string {
	full = strings.TrimSpace(full)
	switch {
	case strings.HasPrefix(full, "refs/heads/"):
		return strings.TrimPrefix(full, "refs/heads/")
	case strings.HasPrefix(full, "refs/remotes/"):
		rest := strings.TrimPrefix(full, "refs/remotes/")
		if i := strings.Index(rest, "/"); i >= 0 {
			return rest[i+1:]
		}
		return rest
	}
	return full
}

// fetchCommitGraph 取回完整提交图（每行「提交 父提交...」）。
//
// 这是分支归属的原料：配合分支顶端提交，就能在内存里把归属沿祖先方向传播下去，
// 从而免掉「逐分支各跑一次 git log」那一大堆进程。
// 只取 hash 和父提交，输出很紧凑；超大仓库用 --max-count 兜底防止内存失控。
func fetchCommitGraph(ctx context.Context, dir string, window scanWindow) string {
	args := []string{"rev-list", "--all", "--parents", "--date-order"}
	if maxGraphCommits > 0 {
		args = append(args, "--max-count="+strconv.Itoa(maxGraphCommits))
	}
	return runGitCtx(ctx, dir, args...)
}

// buildBranchMasks 计算「提交 hash -> 所属分支 bitmask」。
//
// 按提交图输出顺序单遍传播：每个提交的 mask = 自身若是分支顶端则带上自己的 bit，
// 再或上所有子提交的 mask。`--date-order` 保证父提交排在所有子提交之后，
// 所以读到某个提交时它的 mask 已经收齐了。
func buildBranchMasks(refs []branchRef, graphOut string) map[string]uint64 {
	if len(refs) == 0 || graphOut == "" {
		return nil
	}
	tipMask := make(map[string]uint64, len(refs))
	for i, r := range refs {
		if r.Hash == "" {
			continue
		}
		tipMask[r.Hash] |= 1 << uint(i)
	}
	if len(tipMask) == 0 {
		return nil
	}

	masks := make(map[string]uint64, 4096)
	scanner := bufio.NewScanner(strings.NewReader(graphOut))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		hash := fields[0]
		mask := masks[hash] | tipMask[hash]
		if mask == 0 {
			continue
		}
		masks[hash] = mask
		for _, parent := range fields[1:] {
			masks[parent] |= mask
		}
	}
	return masks
}

// branchNamesForMask 把 bitmask 还原成分支名列表（按分支顶端时间从新到旧）
func branchNamesForMask(mask uint64, refs []branchRef) []string {
	if mask == 0 {
		return nil
	}
	names := make([]string, 0, 4)
	for i := range refs {
		if mask&(1<<uint(i)) != 0 {
			names = append(names, refs[i].Name)
		}
	}
	return names
}

// fetchRepoLog 对单个仓库执行 git log 并解析为提交记录（不含分支归属，归属由 applyBranchInfo 补）。
func fetchRepoLog(ctx context.Context, dir string, payload ScanPayload, window scanWindow) []*CommitRecord {
	args := []string{
		"log", "--all",
		"--date=iso-strict",
		"--pretty=tformat:" + gitLogFormat + gitRecordSep,
	}
	if window.sinceArg != "" {
		args = append(args, "--since="+window.sinceArg)
	}
	if window.untilArg != "" {
		args = append(args, "--until="+window.untilArg)
	}
	// 条数上限交给 git 自己截断：以前是全量取回再在 Go 里切，
	// 时间范围选「全部」时会白读整个历史
	if payload.MaxCommits > 0 {
		args = append(args, "--max-count="+strconv.Itoa(payload.MaxCommits))
	}
	out := runGitCtx(ctx, dir, args...)
	if strings.TrimSpace(out) == "" {
		return nil
	}

	records := []*CommitRecord{}
	for _, block := range strings.Split(out, gitRecordSep) {
		block = strings.Trim(block, "\r\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		rec := parseLogLine(strings.Split(block, gitLogFieldSep), dir)
		if rec == nil {
			continue
		}
		records = append(records, rec)
	}
	return records
}

// applyBranchInfo 把分支归属写回提交记录：命中有归属的用归属结果，
// 没命中的（比如分支数超限被裁掉、或提交只被 tag 指向）回落到 ref 推断。
func applyBranchInfo(records []*CommitRecord, refs []branchRef, masks map[string]uint64) {
	for _, rec := range records {
		if names := branchNamesForMask(masks[rec.Hash], refs); len(names) > 0 {
			rec.Branch = names[0]
			rec.BranchCount = len(names)
			rec.BranchInferred = !containsRef(rec.Refs, rec.Branch)
			continue
		}
		if rec.Branch = inferBranchFromRefs(rec.Refs); rec.Branch != "" {
			rec.BranchInferred = false
		}
	}
}

// ==================== 解析 ====================

// parseLogLine 把 git log 一条记录拆成 CommitRecord；字段不足时为解析失败返回 nil。
func parseLogLine(fields []string, dir string) *CommitRecord {
	if len(fields) < 8 {
		return nil
	}
	date := normalizeGitTime(fields[4])
	rec := &CommitRecord{
		Hash:      fields[0],
		ShortHash: fields[1],
		Author:    fields[2],
		Email:     fields[3],
		Date:      date,
		Message:   fields[5],
		Body:      fields[6],
		Refs:      fields[7],
		Project:   filepath.Base(dir),
		Repo:      dir,
		ts:        parseGitTime(date),
	}
	return rec
}

// normalizeGitTime 规整 git --date=iso-strict 输出的时间到 RFC3339
func normalizeGitTime(s string) string {
	// 例如 "2026-09-16 10:00:00 +0800" -> "2026-09-16T10:00:00+08:00"
	ts := strings.TrimSpace(s)
	if ts == "" {
		return ts
	}
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", ts); err == nil {
		return t.Format(time.RFC3339)
	}
	return ts
}

// inferBranchFromRefs 从 git log 的 %D（ref 列，如 "HEAD -> main, origin/main"）解析展示分支
var refNameRe = regexp.MustCompile(`->\s+([^,]+)`)

func inferBranchFromRefs(refs string) string {
	refs = strings.TrimSpace(refs)
	if refs == "" {
		return ""
	}
	if m := refNameRe.FindStringSubmatch(refs); len(m) == 2 {
		b := strings.TrimSpace(m[1])
		if b != "" {
			return b
		}
	}
	// 兜底：取第一个 ref（去掉 tag:、remote 前缀）
	first := strings.Split(refs, ",")[0]
	first = strings.TrimSpace(first)
	first = strings.TrimPrefix(first, "tag: ")
	if first == HEAD {
		return ""
	}
	if i := strings.Index(first, "/"); i > 0 && strings.HasPrefix(first, "origin") {
		return first[i+1:]
	}
	return first
}

// containsRef 判断 refs 串里是否直接包含某分支名（即该提交是分支顶端）
func containsRef(refs, branch string) bool {
	if refs == "" || branch == "" {
		return false
	}
	for _, r := range strings.Split(refs, ",") {
		r = strings.TrimSpace(r)
		if r == branch || r == "HEAD -> "+branch || r == "origin/"+branch {
			return true
		}
	}
	return false
}

// parseGitTime 解析 ISO-8601 时间；失败时返回零值
func parseGitTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ==================== 进度上报 ====================

// progressTracker 汇总扫描进度并节流上报：
// 仓库多、提交多时事件太密集，节流能避免前端反复重渲染。
type progressTracker struct {
	seq     int64
	emit    func(ScanProgress)
	started time.Time

	mu         sync.Mutex
	reposTotal int
	reposDone  int
	commits    int
	lastEmit   time.Time
}

func newProgressTracker(seq int64, emit func(ScanProgress)) *progressTracker {
	return &progressTracker{seq: seq, emit: emit, started: time.Now()}
}

func (t *progressTracker) setReposTotal(n int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.reposTotal = n
	t.mu.Unlock()
	t.report("scan", "", true)
}

// repoFinished 一个仓库扫完：累加提交数并上报
func (t *progressTracker) repoFinished(name string, commits int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.reposDone++
	t.commits += commits
	t.mu.Unlock()
	t.report("scan", name, true)
}

// report 上报进度。force=false 时按 120ms 节流，丢掉过于密集的中间态。
func (t *progressTracker) report(phase, current string, force bool) {
	if t == nil || t.emit == nil {
		return
	}
	t.mu.Lock()
	if !force && time.Since(t.lastEmit) < 120*time.Millisecond {
		t.mu.Unlock()
		return
	}
	t.lastEmit = time.Now()
	p := ScanProgress{
		Seq:        t.seq,
		Phase:      phase,
		ReposTotal: t.reposTotal,
		ReposDone:  t.reposDone,
		Current:    current,
		Commits:    t.commits,
		ElapsedMs:  time.Since(t.started).Milliseconds(),
	}
	t.mu.Unlock()
	t.emit(p)
}
