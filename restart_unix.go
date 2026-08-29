//go:build !windows

// execSelf.go - 平台分离的进程自替换实现。
// Unix 系（linux/darwin/android/mips…）：exec 自身替换进程映像，
// PID 不变、监听端口经优雅关闭后由新映像重新绑定。

package main

import (
	"os"
	"syscall"
)

// execSelf 以自身参数与环境变量重新 exec 可执行文件。
// 成功后不会返回（进程映像已替换）。
func execSelf(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
