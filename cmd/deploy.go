package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/yijiuba/dukou/internal/deploy"
)

type deployOptions struct {
	server     string
	sshPort    int
	sshUser    string
	sshPass    string
	sshKeyFile string
	sudoPass   string

	port     int
	user     string
	password string

	serverOnly bool
	codex      bool
}

func runDeploy(args []string) error {
	var o deployOptions
	fs := flag.NewFlagSet("dukou deploy", flag.ContinueOnError)
	fs.StringVar(&o.server, "server", "", "海外服务器 IP 或域名（必填）")
	fs.IntVar(&o.sshPort, "ssh-port", 22, "SSH 端口")
	fs.StringVar(&o.sshUser, "ssh-user", "root", "SSH 账号")
	fs.StringVar(&o.sshPass, "ssh-password", "", "SSH 密码")
	fs.StringVar(&o.sshKeyFile, "ssh-key", "", "SSH 私钥文件路径（与密码二选一）")
	fs.StringVar(&o.sudoPass, "sudo-password", "", "本机 sudo 密码，免密可留空")
	fs.IntVar(&o.port, "port", 8443, "TLS 加密代理端口")
	fs.StringVar(&o.user, "user", "dukou", "代理用户名")
	fs.StringVar(&o.password, "password", "", "代理密码，留空自动生成")
	fs.BoolVar(&o.serverOnly, "server-only", false, "只配服务器，不配置本机")
	fs.BoolVar(&o.codex, "codex", false, "顺带安装 Codex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.server == "" {
		return fmt.Errorf("缺少 --server（服务器 IP 或域名）")
	}

	req := deploy.Request{
		ServerHost:   o.server,
		SSHPort:      o.sshPort,
		SSHUser:      o.sshUser,
		SSHPassword:  o.sshPass,
		LocalSudo:    o.sudoPass,
		InstallCodex: o.codex,
		Port:         o.port,
		User:         o.user,
		Password:     o.password,
	}

	if o.sshKeyFile != "" {
		b, err := os.ReadFile(o.sshKeyFile)
		if err != nil {
			return fmt.Errorf("读不了私钥文件: %w", err)
		}
		req.SSHKey = string(b)
	}

	fmt.Println("dukou deploy ─ 一键接入")
	fmt.Printf("      服务器 %s:%d   出口端口 %d\n", o.server, o.sshPort, o.port)

	emit := cliEmitter()
	ctx := context.Background()

	var res deploy.Result
	var err error
	if o.serverOnly {
		res, err = deploy.RunServerOnly(ctx, req, emit)
	} else {
		res, err = deploy.Run(ctx, req, emit)
	}
	fmt.Println()

	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return ErrReported
	}

	// ── 成功汇总 ────────────────────────────────────────────────
	fmt.Println("✓ 全部完成")
	fmt.Println()
	fmt.Printf("  系统        %s\n", res.OSPretty)
	fmt.Printf("  代理地址    %s:%d\n", res.ServerAddr, res.Port)
	fmt.Printf("  用户名      %s\n", res.User)
	fmt.Printf("  密码        %s\n", res.Password)
	if res.EgressIP != "" {
		fmt.Printf("  出口 IP     %s\n", res.EgressIP)
	}

	if !o.serverOnly {
		fmt.Println()
		fmt.Println("  本机已可用。验证：")
		fmt.Printf("    curl -x http://127.0.0.1:7890 https://api.ipify.org\n")
	}
	fmt.Println()
	fmt.Println("  请把上面这串密码存进密码管理器。")
	return nil
}

// cliEmitter 把流水线事件转成终端输出。
//
// 界面版是画进度条，命令行版是逐行打日志——同一份内核，两种呈现。
func cliEmitter() deploy.Emitter {
	return func(e deploy.Event) {
		switch e.Kind {
		case deploy.KindStepStart:
			fmt.Printf("\n[ ] %s\n", e.Msg)
		case deploy.KindStepDone:
			fmt.Printf("[✓] %s\n", e.Msg)
		case deploy.KindStepFail:
			fmt.Printf("[✗] %s\n", e.Msg)
			if e.Detail != "" {
				for _, line := range strings.Split(e.Detail, "\n") {
					fmt.Printf("      %s\n", line)
				}
			}
		case deploy.KindLog:
			fmt.Printf("      %s\n", e.Msg)
		}
	}
}
