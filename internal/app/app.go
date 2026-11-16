// Package app 是 App 服务层：跨平台编排，供 desktop/app.go 转发（§六）。
//
// 这里不 import 任何 GUI 包。界面专属的东西（Wails 事件、窗口控制）
// 通过注入的回调进来，同一套服务层将来可以直接给 CLI/Web 版复用。
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/yijiuba/dukou/internal/account"
	"github.com/yijiuba/dukou/internal/store"
)

// EmitFunc 接收服务层向界面推送的事件（事件名 + 载荷）。
// Wails 版实现为 runtime.EventsEmit；CLI 版可以打印到 stdout。
type EmitFunc func(event string, data interface{})

// Service 聚合各领域能力，生命周期由宿主（desktop/main.go）驱动：
// New → Startup → 运行 → Shutdown。
type Service struct {
	muStatus sync.RWMutex // 保护 status.json 的读写
	ctx      context.Context
	store    *store.Store
	emit     EmitFunc

	// 账号服务客户端（懒加载单例）。token 从加密库恢复，见 account.go。
	muAccount sync.Mutex
	acct      *account.Client

	// 心跳协程的停止信号（有效在线累计，契约 E9）。
	hbMu   sync.Mutex
	hbStop chan struct{}

	// 本机代理驱动。nil 表示用真实实现（internal/proxy）；
	// 单测注入 fake 以覆盖切换线路的顺序与回滚，见 proxy_driver.go。
	pdrv proxyDriver

	// AI 工具的安装/卸载互斥：npm 全局目录是共享的，
	// 并发跑两个 npm install 会互相锁住，界面表现为卡死无进度。
	muAI sync.Mutex
}

// New 创建服务并打开本地存储。configDir 为空时用系统默认用户配置目录。
func New(configDir string) (*Service, error) {
	if configDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("无法定位用户配置目录：%v", err)
		}
		configDir = filepath.Join(base, "dukou")
	}
	st, err := store.Open(configDir)
	if err != nil {
		return nil, err
	}
	return &Service{store: st}, nil
}

// Startup 宿主进入事件循环前调用；ctx 之后用于推送事件与取消长任务。
func (s *Service) Startup(ctx context.Context) {
	s.ctx = ctx
}

// SetEmitter 注入事件推送通道。
func (s *Service) SetEmitter(fn EmitFunc) {
	s.emit = fn
}

// Shutdown 释放资源。
//
// 退出前先断开代理：正常退出若把系统代理留在一个已经死掉的端口上，
// 用户关掉 App 后浏览器直接上不了网——这比"崩溃残留"更常发生，
// 也更容易被误判成"这软件有毒"。
func (s *Service) Shutdown() {
	s.stopHeartbeat()
	if st, err := s.ProxyStatus(); err == nil && st.Connected {
		_ = s.Disconnect()
	}
	s.ctx = nil
}

// ConfigDir 返回配置目录路径（界面"设置"页展示用）。
func (s *Service) ConfigDir() string {
	return s.store.Dir()
}
