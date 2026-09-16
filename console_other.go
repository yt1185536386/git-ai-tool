//go:build !windows

package main

import "os/exec"

// hideChildConsole 其它平台不存在「GUI 程序拉起控制台程序会弹窗」的问题，空实现即可
func hideChildConsole(cmd *exec.Cmd) {}
