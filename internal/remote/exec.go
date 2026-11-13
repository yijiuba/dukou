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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// syncBuffer 是并发安全的输出缓冲。
//
// 踩过的坑：把同一个 bytes.Buffer 同时赋给 Stdout 和 Stderr，
// 两条流各由一个 goroutine 拷贝，而 bytes.Buffer 非并发安全——
// 结果是输出随机丢失。表现极具迷惑性：连着问十次 id -u，
// 有几次能拿到 "0"，有几次拿到空串，且 err 始终为 nil。
// 这类问题只能在真机上撞到，本地单测看不出来。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

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
// 所以这里内置了 sudo 密码注入。
//
// 为什么用 SUDO_ASKPASS 而不是 "sudo -S + stdin"：
// 最初的做法是把密码从 stdin 喂给 sudo -S。但只要命令里出现管道或重定向，
// 这个方案立刻失效——
//
//	echo 'xxx' | base64 -d > /etc/sysctl.d/99-dukou.conf
//
// 这里的 ">" 是由**当前非 root shell** 执行的，与 sudo 无关，必然权限不足；
// 而改成 "| sudo tee" 之后，sudo 的 stdin 变成了管道里的数据，
// 密码反而送不进去。两个洞互相堵死。
//
// SUDO_ASKPASS 把口令从数据流上摘了出来：sudo 需要密码时自己去调脚本，
// stdin 完整留给管道，于是 "sudo" 能出现在命令的任何位置。
type Local struct {
	sudoPass string
}

// NewLocal 创建本机执行器。sudoPass 为空表示免密 sudo。
func NewLocal(sudoPass string) *Local {
	return &Local{sudoPass: sudoPass}
}

func (l *Local) Label() string { return "本机" }

func (l *Local) Close() error { return nil }

// sudoWord 匹配作为独立命令出现的 sudo，用来统一改成 sudo -A。
// 前后都要求是分隔符，"--sudo-password" 这类字面量不会被误伤
// （它前面是 "-"，不在分隔符集合里，误改会得到 "---A-password"）。
var sudoWord = regexp.MustCompile(`(^|[\s;&|(])sudo(\s|$)`)

// Run 执行命令。带 sudo 的命令会自动走 askpass 注入密码。
func (l *Local) Run(cmd string) (string, error) {
	real := cmd
	env := os.Environ()
	if l.sudoPass != "" && sudoWord.MatchString(cmd) {
		dir, err := os.MkdirTemp("", "dukou-askpass")
		if err == nil {
			script := filepath.Join(dir, "askpass.sh")
			// 单引号包住变量名，避免密码里的字符被 shell 解释
			body := "#!/bin/sh\nprintf '%s\\n' \"$DUKOU_SUDO_PASS\"\n"
			if werr := os.WriteFile(script, []byte(body), 0o700); werr == nil {
				defer os.RemoveAll(dir)
				env = append(env, "SUDO_ASKPASS="+script, "DUKOU_SUDO_PASS="+l.sudoPass)
				real = rewriteSudo(cmd)
			}
		}
	}

	c := exec.Command("bash", "-c", real)
	c.Env = env

	var buf syncBuffer
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

// rewriteSudo 把命令里每一处 sudo 都改成 sudo -A。
//
// 必须逐处改而不是只改开头：真正会失败的是管道中间那个 sudo，
// 例如 "echo … | base64 -d | sudo tee /etc/…"——它不在行首。
func rewriteSudo(cmd string) string {
	return sudoWord.ReplaceAllString(cmd, "${1}sudo -A${2}")
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
