//go:build !windows

package aitool

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const launcherExt = ".sh"

// writeLauncher 生成 POSIX 启动脚本。
func writeLauncher(dir string, t Tool, proxyAddr, binPath string) (string, error) {
	path := launcherPath(dir, t)
	if err := ensureParent(path); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# 由 dukou 生成：带代理环境变量启动 " + t.Name + "\n")
	for _, kv := range proxyEnv(proxyAddr) {
		k, v, _ := strings.Cut(kv, "=")
		// 值里有 * 和 , —— 单引号包住，避免被 shell 展开成文件名
		b.WriteString("export " + k + "='" + v + "'\n")
	}
	b.WriteString("cd \"$HOME\"\n")
	b.WriteString("exec \"" + binPath + "\"\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		return "", fmt.Errorf("写入 %s 失败：%v", path, err)
	}
	return path, nil
}

// openTerminal 按常见程度依次尝试终端模拟器。
//
// 没有终端时不要尝试"在当前进程里 exec"——dukou 的 stdout 不是终端，
// TUI 会画成一团乱码，比直接报错更难懂。
func openTerminal(script string) error {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("open"); err == nil {
			return exec.Command("open", "-a", "Terminal", script).Start()
		}
	}

	type cand struct {
		bin  string
		args []string
	}
	// -e 语义：xterm 系是「命令」，gnome-terminal 用 -- 分隔更稳
	for _, c := range []cand{
		{"x-terminal-emulator", []string{"-e", script}},
		{"gnome-terminal", []string{"--", script}},
		{"konsole", []string{"-e", script}},
		{"xfce4-terminal", []string{"-e", script}},
		{"xterm", []string{"-e", script}},
	} {
		if _, err := exec.LookPath(c.bin); err != nil {
			continue
		}
		cmd := exec.Command(c.bin, c.args...)
		if err := cmd.Start(); err != nil {
			continue // 装了这个终端但启动失败，继续试下一个
		}
		return nil
	}
	return errors.New("没有找到可用的终端模拟器（试过 x-terminal-emulator / gnome-terminal / konsole / xfce4-terminal / xterm）")
}
