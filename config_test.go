package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 配置文件必须落在 exe 同级目录：绿色版才能连 exe 一起拷贝/备份。
func TestConfigFilePathIsNextToExe(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取 exe 路径失败: %v", err)
	}
	want := filepath.Join(filepath.Dir(exe), "config.json")
	if got := configFilePath(); got != want {
		t.Errorf("配置文件路径应为 exe 同级 %q，实际 %q", want, got)
	}
	if got := (&App{}).configPath(); got != want {
		t.Errorf("configPath 应与 configFilePath 一致，实际 %q", got)
	}
}

// 保存一份配置后，文件应真实出现在 exe 同级目录，并且能被重新读回来。
func TestSaveConfigWritesNextToExe(t *testing.T) {
	target := configFilePath()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Skipf("exe 同级目录不可写，跳过: %v", err)
	}
	// 别覆盖已有配置（正常情况下也不会有）
	backup, hadBackup := readOrNil(target)
	defer func() {
		if hadBackup {
			_ = os.WriteFile(target, backup, 0o644)
			return
		}
		_ = os.Remove(target)
	}()

	a := &App{}
	cfg := defaultConfig()
	cfg.Ai.APIKey = "sk-test-key"
	cfg.Ai.Model = "deepseek-chat"
	cfg.Workspaces = []string{`D:\projects\demo`}
	cfg.Scan.SinceDays = 3

	if _, err := a.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("配置文件未写到 exe 同级目录 %s: %v", target, err)
	}

	got, err := a.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig 失败: %v", err)
	}
	if got.Ai.APIKey != "sk-test-key" || got.Ai.Model != "deepseek-chat" {
		t.Errorf("读回的 ai 配置不对: %+v", got.Ai)
	}
	if len(got.Workspaces) != 1 || got.Workspaces[0] != `D:\projects\demo` {
		t.Errorf("读回的工作区不对: %v", got.Workspaces)
	}
	if got.Scan.SinceDays != 3 {
		t.Errorf("读回的 sinceDays 应为 3，实际 %d", got.Scan.SinceDays)
	}

	// 旧版扁平结构仍可迁移读取
	legacy := []byte(`{"provider":"deepseek","api_key":"old-key","model_name":"deepseek-chat","base_url":"https://api.deepseek.com/v1","workspace_paths":["D:\\projects\\legacy"]}`)
	if err := os.WriteFile(target, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	migrated, err := a.GetConfig()
	if err != nil {
		t.Fatalf("读取旧版配置失败: %v", err)
	}
	if migrated.Ai.APIKey != "old-key" {
		t.Errorf("旧版配置迁移结果不对: %+v", migrated.Ai)
	}
	if len(migrated.Workspaces) != 1 || migrated.Workspaces[0] != `D:\projects\legacy` {
		t.Errorf("旧版工作区未迁移: %v", migrated.Workspaces)
	}
}

// readOrNil 读文件；不存在时返回 (nil, false)
func readOrNil(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}