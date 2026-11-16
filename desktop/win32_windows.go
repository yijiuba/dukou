//go:build gui && windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32                        = syscall.NewLazyDLL("user32.dll")
	procFindWindowW               = user32.NewProc("FindWindowW")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procShowWindow                = user32.NewProc("ShowWindow")
	procSetForegroundWindowUser32 = user32.NewProc("SetForegroundWindow")
)

// ensureMainWindowVisible 兜底：若主窗口仍不可见，直接 Win32 恢复显示并置前。
func ensureMainWindowVisible() {
	const swRestore = 9
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("渡口 · 一键部署"))))
	if hwnd == 0 {
		return
	}
	if vis, _, _ := procIsWindowVisible.Call(hwnd); vis != 0 {
		return // runtime 已成功，不动
	}
	println("[tray] runtime 唤窗未生效，走 Win32 兜底")
	procShowWindow.Call(hwnd, swRestore)
	procSetForegroundWindowUser32.Call(hwnd)
}
