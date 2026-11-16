//go:build gui && !windows

package main

// ensureMainWindowVisible 的非 Windows 占位：
// v0.2 的 GUI 仅在 Windows 上验证，其他平台不做 Win32 兜底，
// 托盘唤窗只依赖 Wails runtime（失败时用户可从任务栏点开）。
func ensureMainWindowVisible() {}
