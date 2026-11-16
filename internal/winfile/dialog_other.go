//go:build !windows

// Package winfile 非 Windows 平台存根：v0.2 桌面版仅 Windows
// （fyne/systray 需 cgo，见 M1 结论），保留符号供跨平台编译。
package winfile

import "errors"

func SaveFileDialog(title, defaultName, filter string) (string, bool, error) {
	return "", false, errors.New("当前平台暂不支持文件对话框")
}

func OpenFileDialog(title, filter string) (string, bool, error) {
	return "", false, errors.New("当前平台暂不支持文件对话框")
}
