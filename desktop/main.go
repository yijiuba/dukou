//go:build gui

package main

import (
	"embed"
	"os"

	"fyne.io/systray"
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
	// 禁掉 WebView2 的页面缩放（Ctrl+滚轮 / 触摸板双指捏合）。
	// 界面是固定布局的桌面 App，缩放后布局视口比窗口大，
	// 整页就能上下左右拖动、四边露出背景条——看起来像"页面没锁住"。
	// 注意：此参数只在 WebView2 浏览器进程首次创建时生效；实测部分版本对
	// Ctrl+滚轮仍放行，真正的拦截在前端 index.html 的 wheel 事件守卫（renderer 层）。
	// 保留用户已自定义的参数，追加而不是覆盖。
	if prev := os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"); prev != "" {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", prev+" --disable-zoom --disable-pinch")
	} else {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-zoom --disable-pinch")
	}

	app := NewApp()

	// R1（§十）：托盘先行启动，与 Wails 主循环共存。
	// 退出路径：托盘"退出渡口" → runtime.Quit → wails.Run 返回 → systray 兜底清理。
	startTray(app)
	defer systray.Quit()

	err := wails.Run(&options.App{
		Title:     "渡口 · 一键部署",
		Width:     1180,
		Height:    780,
		MinWidth:  1100,
		MinHeight: 660,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:     app.startup,
		OnBeforeClose: app.onBeforeClose,
		Bind:          []interface{}{app},
	})
	if err != nil {
		println("启动失败:", err.Error())
	}
}
