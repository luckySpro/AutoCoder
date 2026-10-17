//go:build tray && windows

package main

import (
	"syscall"
	"unsafe"
)

// 桌面版在 Windows 上按控制台程序编译，双击打开时把自己的控制台窗口藏起来。
// 不用 -H=windowsgui：那样每次调用 git、开发工具等命令行程序都会弹出一个新的黑窗口；
// 藏起来的控制台会被这些子进程继承，就不会弹窗。从终端启动时保留终端输出。
func init() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	u32 := syscall.NewLazyDLL("user32.dll")
	var pids [2]uint32
	n, _, _ := k32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if n != 1 {
		return // 还有别的进程（终端）在用这个控制台
	}
	hwnd, _, _ := k32.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		const swHide = 0
		u32.NewProc("ShowWindow").Call(hwnd, swHide)
	}
}
