//go:build gui

package main

import (
	_ "embed"
	"os"
	"time"

	"fyne.io/systray"
)

// 托盘图标：渡口青渐变底 + 白色 D（构建时从 assets 生成，32px 32bpp）。
//
//go:embed assets/tray.ico
var trayIcon []byte

// startTray 在独立 goroutine 里跑托盘消息循环。
//
// R1 验证点（§十）：Wails v2 没有托盘 API，外挂 systray 后两者能否共存——
// Windows 上 systray 自建隐藏窗口和消息泵，与 Wails 的主线程消息循环互不干扰，
// 实测可共存；Linux 的 appindicator 走 D-Bus，理论上也不抢主循环，待 CI/虚拟机复核。
func startTray(a *App) {
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("渡口 · 一键部署")

		mShow := systray.AddMenuItem("打开主界面", "显示渡口窗口")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出渡口", "停止代理并完全退出")

		go func() {
			for {
				select {
				case <-mShow.ClickedCh:
					a.showWindow()
				case <-mQuit.ClickedCh:
					a.quitApp()
					return
				}
			}
		}()

		println("[tray] 托盘就绪")

		// 自测模式：R1 验证用，模拟菜单动作的完整时序，见 docs/dev/build.md
		if os.Getenv("DUKOU_TRAY_SELFTEST") == "1" {
			go func() {
				time.Sleep(3 * time.Second)
				println("[tray] selftest: hide")
				a.hideWindow()
				time.Sleep(3 * time.Second)
				println("[tray] selftest: show")
				a.showWindow()
				time.Sleep(6 * time.Second)
				println("[tray] selftest: show again (after WM_CLOSE)")
				a.showWindow()
			}()
		}
	}, nil)
}
