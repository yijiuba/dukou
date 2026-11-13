package cmd

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/yijiuba/dukou/internal/detect"
)

type doctorOptions struct {
	server string
	port   int
}

func runDoctor(args []string) error {
	var o doctorOptions
	fs := flag.NewFlagSet("dukou doctor", flag.ContinueOnError)
	fs.StringVar(&o.server, "server", "", "出口服务器 IP，给出后会额外测试链路")
	fs.IntVar(&o.port, "port", 8443, "出口服务器端口")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println("dukou doctor ─ 分层诊断")
	fmt.Println()
	fmt.Println("  诊断按链路分层，任一层失败就停下，给出结论与修复命令。")
	fmt.Println("  L1 环境   L2 连通   L3 鉴权   L4 转发   L5 性能")

	fmt.Println()
	fmt.Println("L1 环境")
	profile := detect.Detect(detect.NewEnv())
	for _, line := range profile.Lines() {
		detail("%s", line)
	}

	fmt.Println()
	fmt.Println("L2 连通")
	if o.server == "" {
		detail("跳过（未提供 --server），例如：dukou doctor --server 1.2.3.4")
	} else {
		addr := net.JoinHostPort(o.server, fmt.Sprintf("%d", o.port))
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 8*time.Second)
		if err != nil {
			detail("TCP %s 不可达（%v）", addr, err)
			detail("结论：网络层断了。依次检查云厂商安全组、服务器防火墙、服务是否在跑。")
			return fmt.Errorf("L2 失败")
		}
		conn.Close()
		detail("TCP %s 可达，耗时 %d ms", addr, time.Since(start).Milliseconds())
	}

	fmt.Println()
	fmt.Println("L3-L5")
	detail("尚未实现（v0.1 开发中）：鉴权、转发、性能三层需要本地中转就绪后测试")

	return nil
}
