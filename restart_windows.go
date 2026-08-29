//go:build windows

// execSelf.go - 平台分离的进程自替换实现。
// Windows 无 exec 自替换能力：重启请求由 restartProc 的
// runtime.GOOS 预判拦截并提示手动重启，本实现仅为满足编译。

package main

import "errors"

// execSelf Windows 平台永远不应被调用（restartProc 已同步拦截）。
func execSelf(exe string) error {
	return errors.New("Windows 平台不支持进程内重启，请手动退出后重新启动")
}
