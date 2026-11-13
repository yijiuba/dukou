package cmd

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/yijiuba/dukou/internal/detect"
)

type clientOptions struct {
	server   string
	port     int
	user     string
	password string
	profile  string
	dryRun   bool
}

func runClient(args []string) error {
	var o clientOptions
	fs := flag.NewFlagSet("dukou client", flag.ContinueOnError)
	fs.StringVar(&o.server, "server", "", "出口服务器 IP 或域名（必填）")
	fs.IntVar(&o.port, "port", 8443, "出口服务器的 TLS 代理端口")
	fs.StringVar(&o.user, "user", "dukou", "代理用户名")
	fs.StringVar(&o.password, "password", "", "代理密码")
	fs.StringVar(&o.profile, "profile", "codex", "要安装的 CLI：codex | claude | gemini")
	fs.BoolVar(&o.dryRun, "dry-run", false, "只打印执行计划，不改动系统")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.server == "" {
		return fmt.Errorf("缺少 --server，例：dukou client --server 1.2.3.4")
	}

	fmt.Println("dukou client ─ 接通道并安装 CLI")
	const total = 8

	step(1, total, "环境探测")
	profile := detect.Detect(detect.NewEnv())
	for _, line := range profile.Lines() {
		detail("%s", line)
	}

	step(2, total, "连通性测试（这一步决定后面全部工作是否有意义）")
	addr := net.JoinHostPort(o.server, fmt.Sprintf("%d", o.port))
	conn, err := net.DialTimeout("tcp", addr, 8*time.Second)
	if err != nil {
		return fmt.Errorf("连不上 %s：%w\n      先确认云厂商安全组与服务器防火墙已放行该端口", addr, err)
	}
	conn.Close()
	detail("TCP %s 可达", addr)

	step(3, total, "安装 gost 并配置本地中转")
	detail("127.0.0.1:7890（HTTP）/ 127.0.0.1:7891（SOCKS5）→ %s", addr)

	step(4, total, "写入代理环境变量")
	detail("/etc/environment、/etc/profile.d/、~/.config/environment.d/ 三处")

	step(5, total, "配置包管理器")
	detail("按 %s 家族处理，内网与国内源保持直连", profile.Family)

	step(6, total, "配置浏览器分流")
	detail("本地托管 PAC 于 127.0.0.1:8899，国内域名直连")

	step(7, total, fmt.Sprintf("安装 %s", o.profile))
	detail("Node 与 CLI 按 --profile 选择安装方式")

	step(8, total, "验证门")
	detail("经本地中转访问外网，返回出口服务器 IP 即成功")

	if o.dryRun {
		fmt.Println("\n--dry-run：仅打印计划，未改动系统。")
		return nil
	}
	return fmt.Errorf("步骤 3 之后尚未实现（v0.1 开发中）；可用 --dry-run 查看完整计划")
}
