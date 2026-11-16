//go:build gui

package main

import (
	"context"
	"sync"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/yijiuba/dukou/internal/app"
	"github.com/yijiuba/dukou/internal/deploy"
	"github.com/yijiuba/dukou/internal/store"
)

// App 是暴露给前端的对象。
//
// §十二：app.go 薄——所有编排逻辑都在 internal/app.Service 里，这里只做转发。
// 不用嵌入而是显式转发，是为了把绑定面收紧到 §5.1 契约清单，
// Service 的生命周期方法（Startup/SetEmitter 等）不会漏到前端。
type App struct {
	svc *app.Service

	ctx      context.Context
	quitting bool
	mu       sync.Mutex
}

// NewApp 创建应用实例。
func NewApp(svc *app.Service) *App {
	return &App{svc: svc}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.svc.Startup(ctx)
	// 服务层事件 → Wails 事件通道。事件名规范见 §5.2（deploy:event / proxy:status / ...）
	a.svc.SetEmitter(func(event string, data interface{}) {
		runtime.EventsEmit(ctx, event, data)
	})
}

// ── 转发 internal/app.Service（§5.1 契约方法） ──────────────────────────

// Deploy 执行完整部署流程。进度经服务层事件推给前端，前端不需要轮询。
func (a *App) Deploy(req deploy.Request) (deploy.Result, error) {
	return a.svc.Deploy(req)
}

// ListServers 返回服务器清单（不含机密字段）。
func (a *App) ListServers() ([]store.Server, error) {
	return a.svc.ListServers()
}

// AddServer 新增服务器。密码/私钥只进加密库，不回显。
func (a *App) AddServer(in store.ServerInput) (store.Server, error) {
	return a.svc.AddServer(in)
}

// UpdateServer 编辑服务器。SSHPassword/SSHKey 留空表示保持原凭据。
func (a *App) UpdateServer(id string, in store.ServerInput) (store.Server, error) {
	return a.svc.UpdateServer(id, in)
}

// RemoveServer 删除服务器（含加密凭据）。
func (a *App) RemoveServer(id string) error {
	return a.svc.RemoveServer(id)
}

// ProxyStatus 前端启动时先拉一次连接状态。
func (a *App) ProxyStatus() (app.Status, error) {
	return a.svc.ProxyStatus()
}

// Connect 起本机代理并接管系统代理。
func (a *App) Connect(serverID string) (app.Status, error) {
	return a.svc.Connect(serverID)
}

// Disconnect 停代理并严格还原系统代理。
func (a *App) Disconnect() error {
	return a.svc.Disconnect()
}

// CleanupResidue 一键清理残留代理（上次异常退出留下的系统代理接管）。
func (a *App) CleanupResidue() (app.Status, error) {
	return a.svc.CleanupResidue()
}

// SwitchServer 切换线路：先断开旧线路，再连新线路。
func (a *App) SwitchServer(serverID string) error {
	return a.svc.SwitchServer(serverID)
}

// Doctor 分层体检指定服务器（健康诊断页 / 服务器页「体检」按钮）。
// 调用可能耗时十几秒（SSH + 探测），前端按钮需置 loading。
func (a *App) Doctor(serverID string) (app.DoctorReport, error) {
	return a.svc.Doctor(serverID)
}

// AppInfo 版本/平台/首次启动标记（新手引导只弹一次）。
func (a *App) AppInfo() (app.AppInfo, error) {
	return a.svc.AppInfo()
}

// ConfigDir 返回配置目录（设置页展示用）。
func (a *App) ConfigDir() string {
	return a.svc.ConfigDir()
}

// ── 窗口 / 托盘（GUI 专属，不进服务层） ─────────────────────────────────

// onBeforeClose 拦截窗口关闭：点 X 不退出，隐藏到托盘。
// 返回 true 表示"阻止默认关闭行为"。
// 真正退出只能走托盘菜单"退出渡口"（先置 quitting 再调 runtime.Quit）。
func (a *App) onBeforeClose(ctx context.Context) bool {
	a.mu.Lock()
	q := a.quitting
	a.mu.Unlock()
	if q {
		return false // 允许退出
	}
	runtime.WindowHide(ctx)
	return true
}

// showWindow 从托盘唤回主界面。托盘菜单"打开主界面"调用。
//
// 先走 Wails runtime（带主线程 Invoke、激活、焦点），再用 Win32 兜底：
// runtime 调用在某些窗口状态下可能静默失败（日志看不到、窗口没起来），
// 兜底直接对窗口句柄 SW_RESTORE + SetForegroundWindow，保证"点托盘必出窗"。
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
	ensureMainWindowVisible()
}

var _ = struct{}{} // 平台专属 Win32 辅助见 win32_windows.go / win32_other.go

// hideWindow 隐藏主窗口到托盘。
func (a *App) hideWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowHide(a.ctx)
}

// quitApp 托盘菜单"退出渡口"：置退出标记后真正关闭。
// 走 onBeforeClose 的 quitting 分支，不会被拦截成隐藏。
func (a *App) quitApp() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	if a.ctx != nil {
		systray.Quit() // 先收托盘，再让 Wails 退出
		runtime.Quit(a.ctx)
	}
}
