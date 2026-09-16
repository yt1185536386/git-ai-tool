//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 Windows 的进程创建标志，要求系统不要为新进程创建控制台窗口
const createNoWindow = 0x08000000

// hideChildConsole 让子进程不弹出控制台窗口。
//
// 打包后的 exe 是 GUI 子系统（-H windowsgui），自身没有控制台；
// 这种进程再拉起控制台程序（git.exe）时，Windows 会为每个子进程单独分配一个
// 控制台窗口，于是扫描提交记录时黑框会一个接一个往外弹。
// 加上 CREATE_NO_WINDOW 标志后一切正常，stdout/stderr 管道不受影响。
func hideChildConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
