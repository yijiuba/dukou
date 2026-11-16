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

		// 库的事件投递是 select+default 的非阻塞发送（无人接收时静默丢弃），
		// 所以每个菜单项配一个专职 goroutine，始终停在 <-ClickedCh 上，
		// 把"点击被丢弃"的窗口压到最小。不能合并成一个 select 循环：
		// 处理 A 期间 B 的点击会丢。
		go func() {
			for range mShow.ClickedCh {
				println("[tray] 菜单点击：打开主界面")
				a.showWindow()
			}
		}()
		go func() {
			for range mQuit.ClickedCh {
				println("[tray] 菜单点击：退出渡口")
				a.quitApp()
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
