//go:build gui

package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/yijiuba/dukou/internal/account"
	"github.com/yijiuba/dukou/internal/aitool"
	"github.com/yijiuba/dukou/internal/app"
	"github.com/yijiuba/dukou/internal/deploy"
	"github.com/yijiuba/dukou/internal/store"
	"github.com/yijiuba/dukou/internal/winfile"
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

// shutdown Wails 退出回调：断开代理（还原系统代理）并释放资源。
// 不接这个回调的后果是：用户从托盘退出后，系统代理还指着一个已经关掉的
// 127.0.0.1:7890，浏览器直接全站打不开——最典型的"卸载级差评"来源。
func (a *App) shutdown(ctx context.Context) {
	a.svc.Shutdown()
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

// ── 账号与订阅（v0.3，契约 `账号服务接口契约-v1.0`）─────────────────────

// SessionInfo 当前登录态（启动时先拉一次，决定是否显示登录页）。
func (a *App) SessionInfo() (app.SessionInfo, error) {
	return a.svc.SessionInfo()
}

// LoginRequestCode 发送邮箱验证码（E1）。返回的 verificationId 前端需要保存。
func (a *App) LoginRequestCode(email string) (account.CodeSent, error) {
	return a.svc.LoginRequestCode(email)
}

// LoginVerify 校验验证码完成登录/注册（E2，注册登录合一）。
func (a *App) LoginVerify(email, verificationID string, isExisting bool, code string) (app.LoginResult, error) {
	return a.svc.LoginVerify(email, verificationID, isExisting, code)
}

// Logout 退出登录。
func (a *App) Logout() error {
	return a.svc.Logout()
}

// Account 订阅状态（E4）。
func (a *App) Account() (account.Account, error) {
	return a.svc.Account()
}

// Plans 套餐列表（E6）。
func (a *App) Plans() ([]account.Plan, error) {
	return a.svc.Plans()
}

// ClaimTrial 领取试用（E5）。
func (a *App) ClaimTrial() (account.TrialResult, error) {
	return a.svc.ClaimTrial()
}

// Orders 我的订单（E7）。
func (a *App) Orders() ([]account.Order, error) {
	return a.svc.Orders()
}

// Referral 推广信息（E8）。
func (a *App) Referral() (account.Referral, error) {
	return a.svc.Referral()
}

// ConfigDir 返回配置目录（设置页展示用）。
func (a *App) ConfigDir() string {
	return a.svc.ConfigDir()
}

// ── 备份 / 导出 / 更新（§5.1 契约，v0.2 M3） ───────────────────────────

// ExportBackup 弹「另存为」对话框后导出 .dukoubak。
// cancelled=true 表示用户主动取消，前端不应提示错误。
func (a *App) ExportBackup(password string) (store.BackupSummary, bool, error) {
	path, cancelled, err := winfile.SaveFileDialog("保存 dukou 备份",
		a.svc.SuggestedBackupName(), "dukou 备份包 (*.dukoubak)|*.dukoubak")
	if err != nil {
		return store.BackupSummary{}, false, err
	}
	if cancelled || path == "" {
		return store.BackupSummary{}, true, nil
	}
	sum, err := a.svc.ExportBackup(path, password)
	return sum, false, err
}

// ImportBackup 弹「打开」对话框后导入 .dukoubak。
func (a *App) ImportBackup(password string) (store.BackupSummary, bool, error) {
	path, cancelled, err := winfile.OpenFileDialog("选择 dukou 备份文件",
		"dukou 备份包 (*.dukoubak)|*.dukoubak")
	if err != nil {
		return store.BackupSummary{}, false, err
	}
	if cancelled || path == "" {
		return store.BackupSummary{}, true, nil
	}
	sum, err := a.svc.ImportBackup(path, password)
	return sum, false, err
}

// ExportServers 弹「另存为」对话框后导出脱敏清单（csv / json）。
func (a *App) ExportServers(format string) (int, bool, error) {
	ext := strings.ToLower(strings.TrimPrefix(format, "."))
	name := "dukou-servers-" + time.Now().Format("20060102") + "." + ext
	path, cancelled, err := winfile.SaveFileDialog("导出服务器清单", name,
		strings.ToUpper(ext)+" 文件|*."+ext)
	if err != nil {
		return 0, false, err
	}
	if cancelled || path == "" {
		return 0, true, nil
	}
	n, err := a.svc.ExportServers(path, ext)
	return n, false, err
}

// CheckUpdate 读 GitHub Releases 最新正式版并与本地版本比较。
func (a *App) CheckUpdate() (app.UpdateInfo, error) {
	return a.svc.CheckUpdate()
}

// OpenExternal 一律走系统浏览器打开 HTTP(S) 链接。
//
// 只放行 http/https：前端拼出来的地址若被注入 javascript: 或 file://，
// 直接交给系统打开等于把本机文件系统交出去。
func (a *App) OpenExternal(link string) error {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return errors.New("链接格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("只能打开 http / https 链接")
	}
	runtime.BrowserOpenURL(a.ctx, u.String())
	return nil
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

// ── AI 工具（v0.3 起接管 AI 工具页：检测 / 安装 / 启动 / 卸载） ──
//
// 四个方法都是转发，编排在 internal/app/aitools.go。
// 安装与卸载是长任务，进度经 ai:progress 事件推给界面，
// 返回值只在结束时给最终结果。

// AITools 返回 Codex / Claude Code 的真实检测状态。
func (a *App) AITools() ([]aitool.State, error) {
	return a.svc.AITools()
}

// AIToolInstall 安装指定工具（id: codex | claude）。
func (a *App) AIToolInstall(id string) (aitool.Result, error) {
	return a.svc.AIToolInstall(id)
}

// AIToolUninstall 卸载指定工具。
func (a *App) AIToolUninstall(id string) (aitool.Result, error) {
	return a.svc.AIToolUninstall(id)
}

// AIToolLaunch 在新终端窗口启动指定工具。
func (a *App) AIToolLaunch(id string) (aitool.Result, error) {
	return a.svc.AIToolLaunch(id)
}

// DeployServer 从服务器清单发起部署（「先保存后部署」流程）。
// 表单上改过的连接信息会存回清单；SSHPassword 留空表示沿用已保存的。
func (a *App) DeployServer(id string, in store.ServerInput) (deploy.Result, error) {
	return a.svc.DeployServer(id, in)
}

// RefreshServerGeo 按 IP 归属回填服务器名称与旗帜（只补缺失，不覆盖用户输入）。
func (a *App) RefreshServerGeo(id string) (store.Server, error) {
	return a.svc.RefreshServerGeo(id)
}
