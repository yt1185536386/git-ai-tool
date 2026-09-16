package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ==================== 纯函数单测 ====================

func TestNormalizeGitTime(t *testing.T) {
	cases := map[string]string{
		"2026-09-16 10:00:00 +0800": "2026-09-16T10:00:00+08:00",
		"2026-09-16 10:00:00 -0500": "2026-09-16T10:00:00-05:00",
		"2026-09-16T10:00:00+08:00": "2026-09-16T10:00:00+08:00", // 已是 RFC3339
		"":                          "",
	}
	for in, want := range cases {
		if got := normalizeGitTime(in); got != want {
			t.Errorf("normalizeGitTime(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestInferBranchFromRefs(t *testing.T) {
	cases := map[string]string{
		"HEAD -> main":              "main",
		"HEAD -> main, origin/main": "main",
		"origin/feat/x":             "feat/x",
		"tag: v1.0.0":               "v1.0.0",
		"HEAD":                      "",
		"":                          "",
	}
	for in, want := range cases {
		if got := inferBranchFromRefs(in); got != want {
			t.Errorf("inferBranchFromRefs(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestParseLogLine(t *testing.T) {
	line := "abcd1234abc" + "\x1f" + "abcd1234" + "\x1f" + "张三" + "\x1f" + "zhangsan@example.com" + "\x1f" + "2026-09-16 10:00:00 +0800" + "\x1f" + "feat: 新功能" + "\x1f" + "详情正文" + "\x1f" + "HEAD -> main"
	fields := splitLine(line)
	rec := parseLogLine(fields, `D:\projects\demo`)
	if rec == nil {
		t.Fatal("解析失败")
	}
	if rec.Hash != "abcd1234abc" || rec.ShortHash != "abcd1234" {
		t.Errorf("hash 解析不对: %+v", rec.Hash)
	}
	if rec.Author != "张三" || rec.Email != "zhangsan@example.com" {
		t.Errorf("作者解析不对: %+v", rec)
	}
	if rec.Message != "feat: 新功能" || rec.Body != "详情正文" {
		t.Errorf("消息解析不对: %+v", rec.Message)
	}
	if rec.Project != "demo" {
		t.Errorf("项目名应为仓库目录名: %q", rec.Project)
	}
	// 字段不足应返回 nil
	if parseLogLine([]string{"a", "b"}, `D:\projects\x`) != nil {
		t.Error("字段不足时应返回 nil")
	}
}

// splitLine 用与 gitLogFieldSep 相同的分隔符切行
func splitLine(line string) []string {
	return strings.Split(line, gitLogFieldSep)
}

func TestShortBranchName(t *testing.T) {
	cases := map[string]string{
		"refs/heads/develop":          "develop",
		"refs/heads/feature/x":        "feature/x", // 本地分支名里的 / 要保留
		"refs/remotes/origin/develop": "develop",   // 远端名丢掉，与本地同名分支合并
		"refs/remotes/origin/feat/x":  "feat/x",
		"refs/remotes/upstream/master": "master",
	}
	for in, want := range cases {
		if got := shortBranchName(in); got != want {
			t.Errorf("shortBranchName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestResolveScanWindow(t *testing.T) {
	days := 7
	w := resolveScanWindow(ScanPayload{SinceDays: &days})
	if w.sinceArg == "" || w.sinceTime.IsZero() {
		t.Fatalf("按天数应算出 since: %+v", w)
	}
	w = resolveScanWindow(ScanPayload{Since: "2026-09-10"})
	if !w.sinceTime.Equal(time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)) {
		t.Errorf("YYYY-MM-DD 应按本地时区解析，实际 %v", w.sinceTime)
	}
	zero := 0
	w = resolveScanWindow(ScanPayload{SinceDays: &zero})
	if w.sinceArg != "" || !w.sinceTime.IsZero() {
		t.Errorf("全部时间范围不应有 since: %+v", w)
	}
}

// ==================== 造仓 E2E（依赖真实 git，不可用时跳过） ====================

// TestDiscoverReposAndScan 在一个临时目录下创建真实的 git 仓库，
// 验证 discoverRepos 能找到它并通过 scanWorkspaces 读出提交。
func TestDiscoverReposAndScan(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过造仓测试")
	}
	ws := t.TempDir()
	repo := filepath.Join(ws, "app", "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		out, _ := exec.Command("git", args...).CombinedOutput()
		return string(out)
	}
	// 初始化仓库并提交
	run("-C", repo, "init", "-b", "main")
	run("-C", repo, "config", "user.name", "张三")
	run("-C", repo, "config", "user.email", "zhangsan@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	run("-C", repo, "add", ".")
	run("-C", repo, "commit", "-m", "feat: 初版")

	repos := discoverRepos([]string{ws}, 3)
	if len(repos) != 1 || repos[0] != repo {
		t.Fatalf("discoverRepos 应找到仓库 %q，实际 %v", repo, repos)
	}

	sinceDays := 7
	result := scanWorkspaces([]string{ws}, ScanPayload{SinceDays: &sinceDays, MaxCommits: 50})
	if len(result.Repos) != 1 {
		t.Fatalf("应有 1 个仓库，实际 %d", len(result.Repos))
	}
	if result.Repos[0].Error != "" {
		t.Fatalf("仓库不应有错误: %s", result.Repos[0].Error)
	}
	if len(result.Commits) == 0 {
		t.Fatal("应读到至少 1 条提交")
	}
	rec := result.Commits[0]
	if rec.Message != "feat: 初版" {
		t.Errorf("提交信息不对: %q", rec.Message)
	}
	if rec.Author != "张三" {
		t.Errorf("作者不对: %q", rec.Author)
	}
	if rec.Branch == "" {
		t.Errorf("应推断出分支: %+v", rec)
	}
	if _, err := time.Parse(time.RFC3339, rec.Date); err != nil {
		t.Errorf("date 应为 RFC3339，实际 %q: %v", rec.Date, err)
	}
}

func TestScanWorkspacesEmpty(t *testing.T) {
	result := scanWorkspaces(nil, ScanPayload{})
	if len(result.Warnings) == 0 {
		t.Error("无工作区时应给出 warning")
	}
}

// ==================== 分支归属：新旧算法等价性 ====================

// TestBranchMasksMatchPerBranch 是这次性能改造的核心回归测试。
//
// 新实现用一次提交图 + bitmask 传播算归属，旧实现是逐分支 `git log --format=%H`。
// 这里用「逐分支 rev-list」当标准答案，验证两者得到的归属集合完全一致 ——
// 只要这条能过，说明 29 个进程压成 1 个进程没有换来行为变化。
func TestBranchMasksMatchPerBranch(t *testing.T) {
	repo := newTestRepo(t)
	// main: A-B-C-D；feature 从 C 拉出，含 merge 回来的 E；hotfix 从 B 拉出
	repo.commit("A: init", nil)
	repo.commit("B: 第二版", nil)
	repo.git("checkout", "-b", "feature")
	repo.commit("E: 分支上的提交", nil)
	repo.git("checkout", "main")
	repo.commit("C: 主干继续", nil)
	repo.git("merge", "--no-ff", "-m", "D: merge feature", "feature")
	repo.git("checkout", "-b", "hotfix", "HEAD~2")
	repo.commit("F: 热修", nil)
	repo.git("checkout", "main")

	ctx := context.Background()
	refs := listBranchRefs(ctx, repo.dir, time.Time{})
	if len(refs) != 3 {
		t.Fatalf("应有 3 个分支，实际 %d: %+v", len(refs), refs)
	}

	masks := buildBranchMasks(refs, fetchCommitGraph(ctx, repo.dir, scanWindow{}))
	if len(masks) == 0 {
		t.Fatal("提交图传播结果为空")
	}

	// 标准答案：逐分支 rev-list
	expected := map[string]map[string]bool{}
	allHashes := map[string]bool{}
	for _, r := range refs {
		set := map[string]bool{}
		for _, h := range strings.Split(repo.git("rev-list", r.Name), "\n") {
			if h = strings.TrimSpace(h); h != "" {
				set[h] = true
				allHashes[h] = true
			}
		}
		expected[r.Name] = set
	}

	for hash := range allHashes {
		want := map[string]bool{}
		for name, set := range expected {
			if set[hash] {
				want[name] = true
			}
		}
		got := map[string]bool{}
		for _, name := range branchNamesForMask(masks[hash], refs) {
			got[name] = true
		}
		if len(got) != len(want) {
			t.Errorf("提交 %s 归属不符：新=%v 旧=%v", hash[:8], keys(got), keys(want))
			continue
		}
		for name := range want {
			if !got[name] {
				t.Errorf("提交 %s 归属不符：新=%v 旧=%v", hash[:8], keys(got), keys(want))
			}
		}
	}
}

// TestBranchMasksCoversMergedBranch 单独盯一下 merge 场景：
// 被 merge 进来的分支上的提交，归属里必须同时有主干和被合并分支。
func TestBranchMasksCoversMergedBranch(t *testing.T) {
	repo := newTestRepo(t)
	repo.commit("A: init", nil)
	repo.git("checkout", "-b", "feature")
	featureTip := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	repo.commit("E: 分支提交", nil)
	repo.git("checkout", "main")
	repo.git("merge", "--no-ff", "-m", "D: merge", "feature")

	ctx := context.Background()
	refs := listBranchRefs(ctx, repo.dir, time.Time{})
	masks := buildBranchMasks(refs, fetchCommitGraph(ctx, repo.dir, scanWindow{}))

	names := branchNamesForMask(masks[featureTip], refs)
	if len(names) != 2 {
		t.Fatalf("merge 后 feature 上的提交应属于 2 个分支，实际 %v", names)
	}
}

// TestBodyKeepsNewlines 提交正文里的换行不能被当成「另一条提交」而丢掉
func TestBodyKeepsNewlines(t *testing.T) {
	repo := newTestRepo(t)
	repo.git("commit", "--allow-empty", "-m", "feat: 多行正文", "-m", "第一行\n第二行\n第三行")

	days := 3650
	result := scanWorkspaces([]string{repo.dir}, ScanPayload{SinceDays: &days, MaxCommits: 50})
	if len(result.Commits) != 1 {
		t.Fatalf("应解析出 1 条提交，实际 %d", len(result.Commits))
	}
	body := result.Commits[0].Body
	for _, want := range []string{"第一行", "第二行", "第三行"} {
		if !strings.Contains(body, want) {
			t.Errorf("正文缺少 %q，实际 %q", want, body)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ==================== 测试用仓库脚手架 ====================

type testRepo struct {
	t   *testing.T
	dir string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过造仓测试")
	}
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &testRepo{t: t, dir: dir}
	r.git("init", "-b", "main")
	r.git("config", "user.name", "张三")
	r.git("config", "user.email", "zhangsan@example.com")
	r.git("config", "commit.gpgsign", "false")
	return r
}

// git 执行 git 命令并返回 stdout，失败直接 fail
func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("git %v 失败: %v\n%s", args, err, errBuf.String())
	}
	return out.String()
}

// commit 造一条提交；files 为 nil 时造空提交（方便造出多个提交）
func (r *testRepo) commit(message string, files map[string]string) {
	r.t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
		r.git("add", name)
	}
	r.git("commit", "--allow-empty", "-m", message)
}