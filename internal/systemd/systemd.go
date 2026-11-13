// Package systemd 生成并管理 dukou 的 systemd 单元。
//
// 单元内容全部由纯函数渲染，便于单元测试逐字比对。
// 之所以不用「先写模板文件再 sed 替换」，是因为本次排障中 sed 匹配不到时
// 会静默跳过、restart 照样成功，是最难发现的一类故障。
package systemd

import (
	"fmt"
	"strings"
)

// Unit 描述一个待安装的 systemd 单元。
type Unit struct {
	Name        string   // 不含 .service 后缀
	Description string
	ExecStart   string   // 完整命令行
	ExtraUnit   []string // [Unit] 段附加指令
	ExtraSvc    []string // [Service] 段附加指令
}

// ServiceName 按角色返回服务名。
// 服务端与客户端分开命名，万一装在同一台机器上也能共存。
func ServiceName(role string) string {
	if role == "client" {
		return "dukou-client"
	}
	return "dukou"
}

// Path 返回单元文件路径。
func (u Unit) Path() string {
	return "/etc/systemd/system/" + u.Name + ".service"
}

// Render 生成单元文件内容。
func (u Unit) Render() []byte {
	var b strings.Builder

	b.WriteString("# 由 dukou 生成，请勿手工修改：改动会在下次部署时被覆盖\n")
	b.WriteString("# 要调整参数，改 /etc/dukou.conf 后执行 systemctl restart " + u.Name + "\n\n")

	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", u.Description)
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	for _, line := range u.ExtraUnit {
		b.WriteString(line + "\n")
	}

	b.WriteString("\n[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", u.ExecStart)
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=3\n")
	b.WriteString("LimitNOFILE=65535\n")
	// 温和加固：这些选项不影响 gost 正常工作，
	// 但能限制一旦被利用时可造成的破坏范围。
	b.WriteString("NoNewPrivileges=true\n")
	b.WriteString("PrivateTmp=true\n")
	for _, line := range u.ExtraSvc {
		b.WriteString(line + "\n")
	}

	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return []byte(b.String())
}

// ServerUnit 生成服务端单元：只开一个 TLS 加密端口。
//
// 这里只用 http+tls 一个监听器，不是笔误：明文 HTTP 代理会在跨境链路上
// 以明文发送 "CONNECT 域名:443"，中间设备读到域名后会立即返回 RST。
// 加上 TLS 后这段内容不可见，这是整套方案能用的关键。
func ServerUnit(binPath string, port int, user, password string) Unit {
	return Unit{
		Name:        ServiceName("server"),
		Description: "dukou proxy server (gost)",
		ExecStart: fmt.Sprintf("%s -L http+tls://%s:%s@:%d",
			binPath, user, password, port),
	}
}

// ClientUnit 生成客户端单元：本机无鉴权监听 7890/7891，向上游走 TLS。
//
// 本机监听不带鉴权是有意的：这样浏览器、apt、Codex 等所有软件都指向
// 127.0.0.1，密码在整个系统中只出现一次，轮换时只需改这里。
// 同时 7890 只绑定 loopback，外部无法访问。
func ClientUnit(binPath, server string, port int, user, password string,
	httpPort, socksPort int) Unit {
	return Unit{
		Name:        ServiceName("client"),
		Description: "dukou local relay to overseas egress server",
		ExecStart: fmt.Sprintf(
			"%s -L http://127.0.0.1:%d -L socks5://127.0.0.1:%d -F tls://%s:%s@%s:%d",
			binPath, httpPort, socksPort, user, password, server, port),
	}
}

// EnableCommands 返回启用服务所需的命令序列。
func (u Unit) EnableCommands() [][]string {
	return [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", u.Name},
	}
}

// DisableCommands 返回停用服务所需的命令序列，顺序与启用相反。
func (u Unit) DisableCommands() [][]string {
	return [][]string{
		{"systemctl", "disable", "--now", u.Name},
		{"systemctl", "daemon-reload"},
	}
}
