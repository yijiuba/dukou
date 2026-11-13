//go:build gui

package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// assets 内嵌前端。前端是纯静态 HTML，不需要 npm 构建，
// 这样别人 clone 下来不用装 Node 就能编译出桌面程序。
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "渡口 · 一键部署",
		Width:     940,
		Height:    760,
		MinWidth:  820,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind:      []interface{}{app},
	})
	if err != nil {
		println("启动失败:", err.Error())
	}
}
