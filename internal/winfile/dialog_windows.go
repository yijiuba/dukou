//go:build windows

// Package winfile 封装 comdlg32 标准文件对话框。
//
// 不用 Wails runtime.SaveFileDialog 的原因：Wails v2.10.1 的对话框走
// go-common-file-dialog（COM IFileSaveDialog），与 WebView2 组合存在真实
// 崩溃——对话框弹出期间主窗口收到 WM_ACTIVATE，WebView2 的 Focus 回调
// 返回 E_INVALIDARG，应用整体退出（表现为「点立即备份、填完密码闪退」，
// 堆栈见 0.2.0 验收记录）。升级 go-webview2 到 v1.0.23 会让 WebView2
// 环境初始化直接失败，升级 wails 大版本影响面太大。
// comdlg32 的 GetSaveFileNameW 是系统记事本同款 API，不经过 WebView2。
//
// 两条在真机上踩出来的坑（都体现为进程无 panic 直接退出）：
//  1. owner 不能是主线程的窗口——绑定方法跑在 Wails 桥接 goroutine，
//     跨线程 owner 是未定义行为。因此 owner 恒为 0，对话框独立弹出。
//  2. Explorer 风格对话框要求 STA，而桥接 goroutine 已被 Wails 初始化
//     成 MTA。必须在新线程上 runtime.LockOSThread 后再 CoInitializeEx。
package winfile

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ofnOverwritePrompt = 0x00000002
	ofnHideReadOnly    = 0x00000004
	ofnFileMustExist   = 0x00001000
	ofnPathMustExist   = 0x00000800

	coinitApartmentThreaded = 2
	rpcEChangedMode         = 0x80010106
)

type openFileNameW struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	PVReserved    uintptr
	DWReserved    uint32
	FlagsEx       uint32
}

var (
	comdlg32            = windows.NewLazySystemDLL("comdlg32.dll")
	procGetSaveFileName = comdlg32.NewProc("GetSaveFileNameW")
	procGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
	ole32               = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx  = ole32.NewProc("CoInitializeEx")
)

// utf16FromString 编码为 NUL 结尾的 UTF-16。与 syscall.UTF16FromString
// 不同：允许调用方字符串自带内嵌 NUL（dosFilter 依赖它拼 \0 分隔段），
// 只保证结果以额外的一个 NUL 收尾。
func utf16FromString(s string) []uint16 {
	u := make([]uint16, 0, len(s)+1)
	for _, r := range s {
		u = append(u, uint16(r)) // filter/title 由本包拼装，只会出现 BMP 字符
	}
	return append(u, 0)
}

// dosFilter 把 "显示名1|*.ext1|显示名2|*.ext2" 转成 Win32 需要的
// "显示名1\0*.ext1\0显示名2\0*.ext2\0\0"。
func dosFilter(filter string) string {
	out := ""
	for _, p := range splitPipe(filter) {
		out += p + "\x00"
	}
	return out + "\x00"
}

func splitPipe(filter string) []string {
	var parts []string
	cur := ""
	for _, r := range filter {
		if r == '|' {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

// dlgJob 一次对话框调用的入参与出参（跨 goroutine 传递）。
type dlgJob struct {
	save        bool
	title       string
	defaultName string
	filter      string

	path      string
	cancelled bool
	err       error
}

// runFileDialog 在专用 STA 线程上弹对话框并等待结果。
func runFileDialog(job *dlgJob) {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread() // COM apartment 与消息循环都绑定本线程
		defer runtime.UnlockOSThread()
		defer close(done)

		hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
		staOK := hr == 0 || hr == rpcEChangedMode
		if hr != 0 && hr != rpcEChangedMode {
			job.err = fmt.Errorf("COM 初始化失败（0x%x）", hr)
			return
		}
		if staOK && hr == 0 {
			defer procCoInitializeEx.Call(0, 1) // CoUninitialize，仅配对成功初始化
		}

		filter16 := utf16FromString(dosFilter(job.filter))
		var fileBuf [32768]uint16 // MAX_PATH 的 8 倍，防长路径截断
		if job.defaultName != "" {
			copy(fileBuf[:], utf16FromString(job.defaultName))
		}
		title16 := utf16FromString(job.title)
		defExt16 := utf16FromString("dukoubak")

		ofn := openFileNameW{
			StructSize: uint32(unsafe.Sizeof(openFileNameW{})),
			Owner:      0, // 跨线程 owner 是未定义行为，恒为无 owner
			Filter:     &filter16[0],
			File:       &fileBuf[0],
			MaxFile:    uint32(len(fileBuf)),
			Title:      &title16[0],
			DefExt:     &defExt16[0],
		}
		if job.save {
			ofn.Flags = ofnOverwritePrompt | ofnPathMustExist | ofnHideReadOnly
		} else {
			ofn.Flags = ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly
		}

		var ret uintptr
		if job.save {
			ret, _, _ = procGetSaveFileName.Call(uintptr(unsafe.Pointer(&ofn)))
		} else {
			ret, _, _ = procGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
		}
		if ret == 0 {
			job.cancelled = true // 含用户取消与 CDERR_*，统一按取消处理
			return
		}
		end := 0
		for end < len(fileBuf) && fileBuf[end] != 0 {
			end++
		}
		job.path = syscall.UTF16ToString(fileBuf[:end])
		if job.path == "" {
			job.cancelled = true
		}
	}()
	<-done
}

// SaveFileDialog 另存为对话框。defaultName 可为空；cancelled 表示用户取消。
func SaveFileDialog(title, defaultName, filter string) (string, bool, error) {
	if title == "" {
		return "", false, errors.New("对话框标题不能为空")
	}
	job := &dlgJob{save: true, title: title, defaultName: defaultName, filter: filter}
	runFileDialog(job)
	if job.err != nil {
		return "", false, fmt.Errorf("打开保存对话框失败：%v", job.err)
	}
	return job.path, job.cancelled, nil
}

// OpenFileDialog 打开文件对话框。cancelled 表示用户取消。
func OpenFileDialog(title, filter string) (string, bool, error) {
	if title == "" {
		return "", false, errors.New("对话框标题不能为空")
	}
	job := &dlgJob{save: false, title: title, filter: filter}
	runFileDialog(job)
	if job.err != nil {
		return "", false, fmt.Errorf("打开文件对话框失败：%v", job.err)
	}
	return job.path, job.cancelled, nil
}
