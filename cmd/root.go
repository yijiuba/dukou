package cmd

import (
	"errors"
	"fmt"
	"runtime"
)

// Version 可在构建时用 -ldflags "-X github.com/yijiuba/dukou/cmd.Version=x.y.z" 覆盖。
var Version = "0.1.0-dev"

// ErrReported 表示错误已经以人类可读的方式打印过，入口不必再重复一遍。
var ErrReported = errors.New("already reported")

const tagline = "dukou —— 国内机器一键接入海外大模型（运行网络 + 自动化安装）"

type subcommand struct {
	name    string
	summary string
	run     func([]string) error
}

var commands = []subcommand{
	{name: "deploy", summary: "一键部署：SSH 远程装服务端 + 装本机客户端", run: runDeploy},
	{name: "server", summary: "在出口服务器上部署代理服务端", run: runServer},
	{name: "client", summary: "在受控机器上接通道并安装 CLI", run: runClient},
	{name: "doctor", summary: "分层诊断，定位断点并给出修复命令", run: runDoctor},
	{name: "rotate-password", summary: "生成随机密码并同步两端", run: runRotatePassword},
	{name: "selfcheck", summary: "快速自检：服务、拥塞算法、分流、CLI", run: runSelfcheck},
	{name: "uninstall", summary: "按 manifest 清单精确回滚", run: runUninstall},
}

// Execute 是命令分发入口。
func Execute(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
		return nil
	case "version", "-v", "--version":
		fmt.Printf("dukou %s  (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		return nil
	}

	for _, c := range commands {
		if c.name == args[0] {
			return c.run(args[1:])
		}
	}
	return fmt.Errorf("未知子命令 %q，运行 dukou help 查看全部命令", args[0])
}

func printHelp() {
	fmt.Println(tagline)
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  dukou <命令> [参数]")
	fmt.Println()
	fmt.Println("命令:")

	width := 0
	for _, c := range commands {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range commands {
		fmt.Printf("  %-*s  %s\n", width, c.name, c.summary)
	}

	fmt.Println()
	fmt.Println("全局开关:")
	fmt.Println("  --dry-run     只打印执行计划，不改动系统")
	fmt.Println("  --takeover    显式授权接管被占用的端口")
	fmt.Println("  --port N      指定端口，避开冲突")
	fmt.Println()
	fmt.Println("典型用法:")
	fmt.Println("  dukou server --port 8443                      # 出口服务器")
	fmt.Println("  dukou client --server 1.2.3.4 --port 8443     # 受控机器")
	fmt.Println("  dukou doctor                                  # 出问题时先跑这个")
}
