//go:build gui

package main

import (
	"context"
	"sync"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/yijiuba/dukou/internal/deploy"
)

// App 是暴露给前端的对象。
//
// 这里刻意保持极薄：所有编排逻辑都在 internal/deploy 里，
// 这样同一套逻辑将来给 CLI 或 Web 版复用，而界面只负责收集输入、显示进度。
type App struct {
	ctx      context.Context
	quitting bool
	mu       sync.Mutex
}

// NewApp 创建应用实例。
func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

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
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

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

// Deploy 执行完整部署流程。
// 进度通过事件推给前端，前端不需要轮询。
func (a *App) Deploy(req deploy.Request) (deploy.Result, error) {
	emit := func(ev deploy.Event) {
		runtime.EventsEmit(a.ctx, "deploy:event", ev)
	}
	return deploy.Run(a.ctx, req, emit)
}
