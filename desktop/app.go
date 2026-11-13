//go:build gui

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/yijiuba/dukou/internal/deploy"
)

// App 是暴露给前端的对象。
//
// 这里刻意保持极薄：所有编排逻辑都在 internal/deploy 里，
// 这样同一套逻辑将来给 CLI 或 Web 版复用，而界面只负责收集输入、显示进度。
type App struct {
	ctx context.Context
}

// NewApp 创建应用实例。
func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Deploy 执行完整部署流程。
// 进度通过事件推给前端，前端不需要轮询。
func (a *App) Deploy(req deploy.Request) (deploy.Result, error) {
	emit := func(ev deploy.Event) {
		runtime.EventsEmit(a.ctx, "deploy:event", ev)
	}
	return deploy.Run(a.ctx, req, emit)
}
