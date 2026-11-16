//go:build !windows

// 非 Windows 平台的占位实现。
//
// Linux 的「常驻连接」目前在 internal/deploy/client.go 里由 systemd 承担，
// macOS 尚未实现（§四 排在 M2 的后半段）。这里只保证跨平台编译通过，
// 并在被误调用时给出准确的话，而不是一个 nil panic。

package proxy

import (
	"fmt"
	"runtime"
)

// ErrNotImplemented 表示当前平台还没有常驻连接能力。
var ErrNotImplemented = fmt.Errorf("%s 上的常驻代理尚未实现（Windows 已支持）", runtime.GOOS)

// Start 在非 Windows 平台不可用。
func Start(up Upstream, o Options, fetch Fetcher) (Status, error) {
	return Status{}, ErrNotImplemented
}

// Stop 在非 Windows 平台不可用。
func Stop(o Options) error { return ErrNotImplemented }

// Current 在非 Windows 平台恒为未连接。
//
// 不返回错误：前端启动时会无条件拉一次状态，
// 这里报错会让界面显示一个没有意义的红色告警。
func Current(o Options) (Status, error) { return Status{}, nil }

// EnsureBinary 在非 Windows 平台不可用。
func EnsureBinary(o Options, fetch Fetcher) (string, error) {
	return "", ErrNotImplemented
}

// systemPACIsOurs 在非 Windows 平台恒为 false（PAC 分流仅 Windows 实现）。
func SystemPACIsOurs(o Options) bool { return false }
