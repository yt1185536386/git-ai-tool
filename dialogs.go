package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ==================== 系统对话框与文件操作 ====================
//
// 对应 Electron 版的 save-report / open-config-dir / pick-directory 三个 IPC；
// 目录选择用于配置扫描工作区。

// PickDirectory 弹出目录选择对话框，返回选中路径（取消返回空串）
func (a *App) PickDirectory() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("应用尚未完成初始化，请稍后重试")
	}
	path, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "选择工作区目录",
	})
	if err != nil {
		return "", fmt.Errorf("打开目录选择对话框失败：%w", err)
	}
	return path, nil
}

// SaveReport 弹出保存对话框并把报告写入磁盘
func (a *App) SaveReport(filename, content string) (*SaveResult, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("应用尚未完成初始化，请稍后重试")
	}
	if strings.TrimSpace(filename) == "" {
		filename = "git-report.md"
	}
	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "保存报告",
		DefaultFilename: filename,
		Filters: []wruntime.FileFilter{
			{DisplayName: "Markdown", Pattern: "*.md"},
			{DisplayName: "纯文本", Pattern: "*.txt"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("打开保存对话框失败：%w", err)
	}
	if strings.TrimSpace(path) == "" {
		return &SaveResult{OK: false, Canceled: true}, nil
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("写入文件失败：%w", err)
	}
	return &SaveResult{OK: true, Path: path}, nil
}

// OpenConfigDir 在资源管理器中定位配置文件
func (a *App) OpenConfigDir() error {
	path := a.configPath()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("配置文件尚不存在：%s", path)
	}
	switch runtime.GOOS {
	case "windows":
		// explorer 的 /select 参数只接受反斜杠路径
		return exec.Command("explorer", "/select,"+filepath.FromSlash(path)).Start()
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}
