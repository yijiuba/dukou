// Package remote 提供「在哪儿执行」的抽象。
//
// 图形界面要求用户只在一台机器上操作，所以同一条部署逻辑必须既能跑在本机，
// 也能跑在千里之外的服务器上。这里把两者收敛成同一个接口：执行一条 shell 命令，
// 拿回输出。用 shell 命令行而不是 argv 数组，是因为 SSH 走的就是 shell，
// 两边语义一致——argv 数组在远端还得自己拼引号，反而更容易出错。
package remote

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Exec 是执行一条 shell 命令的能力。
type Exec interface {
	// Run 执行命令并返回合并后的 stdout+stderr。
	// 命令失败时返回的 error 里带上输出，便于界面直接展示。
	Run(cmd string) (string, error)
	// Label 用于日志前缀，例如 "server" 或 "local"。
	Label() string
	// Close 释放底层连接。
	Close() error
}

// Local 在本机执行命令。
//
// 图形界面通常以普通用户身份运行，而安装 systemd 单元需要 root，
// 所以这里内置了 sudo 密码注入：命令里写 "sudo ..." 即可，
// Local 会自动改成 "sudo -S" 并把密码从 stdin 喂进去。
type Local struct {
	sudoPass string
}

// NewLocal 创建本机执行器。sudoPass 为空表示免密 sudo。
func NewLocal(sudoPass string) *Local {
	return &Local{sudoPass: sudoPass}
}

func (l *Local) Label() string { return "本机" }

func (l *Local) Close() error { return nil }

// Run 执行命令。带 sudo 前缀的命令会自动注入密码。
func (l *Local) Run(cmd string) (string, error) {
	needsSudo := strings.HasPrefix(strings.TrimSpace(cmd), "sudo ")
	real := cmd
	if needsSudo {
		// -S 从 stdin 读密码，-p '' 抑制提示符，避免污染输出
		real = strings.Replace(cmd, "sudo ", "sudo -S -p '' ", 1)
	}

	c := exec.Command("bash", "-c", real)
	if needsSudo && l.sudoPass != "" {
		c.Stdin = strings.NewReader(l.sudoPass + "\n")
	}

	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf

	done := make(chan error, 1)
	if err := c.Start(); err != nil {
		return "", fmt.Errorf("启动命令失败: %w", err)
	}
	go func() { done <- c.Wait() }()

	select {
	case err := <-done:
		out := buf.String()
		if err != nil {
			return out, fmt.Errorf("%s: %w", firstLine(out), err)
		}
		return out, nil
	case <-time.After(3 * time.Minute):
		_ = c.Process.Kill()
		return buf.String(), fmt.Errorf("命令超时（3 分钟）：%s", cmd)
	}
}

// firstLine 取输出的第一行非空内容，用于把冗长的报错压成一句人话。
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "[sudo]") {
			return line
		}
	}
	return "命令失败"
}
