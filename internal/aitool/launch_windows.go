//go:build windows

package aitool

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const launcherExt = ".cmd"

// writeLauncher 生成 Windows 启动脚本。
//
// 用 call 而不是直接写 codex：npm 装出来的 codex 本身是个 .cmd，
// 在批处理里不加 call 会直接跳走，后面的提示语永远不执行，
// 用户看到的是一个闪退的窗口。
func writeLauncher(dir string, t Tool, proxyAddr, binPath string) (string, error) {
	path := launcherPath(dir, t)
	if err := ensureParent(path); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("chcp 65001 >nul\r\n") // 中文提示别显示成乱码
	b.WriteString("title dukou - " + t.Name + "\r\n")
	for _, kv := range proxyEnv(proxyAddr) {
		k, v, _ := strings.Cut(kv, "=")
		b.WriteString("set \"" + k + "=" + v + "\"\r\n")
	}
	b.WriteString("cd /d \"%USERPROFILE%\"\r\n")
	b.WriteString("call \"" + binPath + "\"\r\n")
	b.WriteString("echo.\r\n")
	// 不 pause 的话，工具退出（比如报错）窗口立刻消失，用户什么都看不到
	b.WriteString("echo [dukou] " + t.Name + " 已退出，按任意键关闭窗口。\r\n")
	b.WriteString("pause >nul\r\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("写入 %s 失败：%v", path, err)
	}
	return path, nil
}

// openTerminal 用 start 拉起一个新控制台窗口。
//
// 不加 HideWindow 的话，GUI 进程起 cmd 会先闪一个黑框；
// 新窗口是 start 创建的，不受这个标志影响。
func openTerminal(script string) error {
	c := exec.Command("cmd.exe", "/c", "start", "", script)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c.Start()
}
