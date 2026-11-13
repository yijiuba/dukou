package cmd

import (
	"flag"
	"fmt"

	"github.com/yijiuba/dukou/internal/detect"
)

type serverOptions struct {
	port     int
	user     string
	password string
	dryRun   bool
	takeover bool
}

func runServer(args []string) error {
	var o serverOptions
	fs := flag.NewFlagSet("dukou server", flag.ContinueOnError)
	fs.IntVar(&o.port, "port", 8443, "TLS 加密代理端口，对外只开这一个")
	fs.StringVar(&o.user, "user", "dukou", "代理用户名")
	fs.StringVar(&o.password, "password", "", "代理密码，留空则自动生成 16 位随机串")
	fs.BoolVar(&o.dryRun, "dry-run", false, "只打印执行计划，不改动系统")
	fs.BoolVar(&o.takeover, "takeover", false, "授权接管被占用的端口")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println("dukou server ─ 部署代理服务端")
	const total = 6

	step(1, total, "环境探测")
	profile := detect.Detect(detect.NewEnv())
	for _, line := range profile.Lines() {
		detail("%s", line)
	}
	if !profile.IsRoot {
		return fmt.Errorf("部署服务端需要 root 权限，请用 sudo 重新执行")
	}
	if profile.Init != detect.Systemd {
		return fmt.Errorf("当前初始化系统为 %s，v0.1 只支持 systemd", profile.Init)
	}

	step(2, total, "端口检查")
	if err := checkPortFree(o.port); err != nil {
		if !o.takeover {
			return fmt.Errorf("%v；确认可接管后加 --takeover 重新执行", err)
		}
		detail("端口 %d 被占用，已按 --takeover 授权接管", o.port)
	} else {
		detail(":%d 空闲", o.port)
	}

	step(3, total, "安装 gost")
	if pkg := profile.Family.PkgCmd(); pkg != "" {
		detail("优先通道：%s（%s 家族）", pkg, profile.Family)
	} else {
		detail("兜底通道：静态二进制（%s 家族无原生包管理）", profile.Family)
	}
	detail("架构：%s", profile.Arch)

	step(4, total, "写入 /etc/dukou.conf")
	detail("端口 %d，用户 %s", o.port, o.user)

	step(5, total, "注册 systemd 服务")
	detail("dukou.service，Restart=always，开机自启")

	step(6, total, "验证门")
	detail("本机 curl 经代理访问外网，返回 401 即链路正常")

	if o.dryRun {
		fmt.Println("\n--dry-run：仅打印计划，未改动系统。")
		return nil
	}
	return fmt.Errorf("步骤 3 之后尚未实现（v0.1 开发中）；可用 --dry-run 查看完整计划")
}
