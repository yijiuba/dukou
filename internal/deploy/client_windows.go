//go:build windows

package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yijiuba/dukou/internal/proxy"
	"github.com/yijiuba/dukou/internal/remote"
)

// setupClient 在 Windows 上完成第 3 步：起本机转发进程并接管系统代理。
//
// 与 Linux 版的根本差异：Linux 用 systemd 托管常驻服务，
// Windows 没有等价物，进程由本程序自己持有（见 internal/proxy）。
// 因此这里不再需要 sudo，也不需要写 .bashrc。
func setupClient(ctx context.Context, _ *remote.Local, req Request, emit Emitter) (string, error) {
	step := string(StepClient)

	dir, err := dataDir()
	if err != nil {
		return "", err
	}

	up := proxy.Upstream{
		Host:     req.ServerHost,
		Port:     req.Port,
		User:     req.User,
		Password: req.Password,
	}

	emit(Event{Kind: KindLog, Step: step, Msg: "检查本机转发程序…"})
	st, err := proxy.Start(up, proxy.Options{DataDir: dir}, sshFetcher(req, emit))
	if err != nil {
		return "", err
	}

	emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf(
		"系统代理已指向 127.0.0.1:%d（含 SOCKS 通道）", st.LocalPort)})
	emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf(
		"自检通过，往返 %d ms", st.LatencyMS)})

	return st.EgressIP, nil
}

// sshFetcher 借道刚装好的服务器下载 gost。
//
// 本机此刻还没有任何代理，直连 GitHub 基本不通；
// 而服务器刚刚验证过能直连海外，它就是唯一可用的出口。
// 走 SSH 而不是 HTTP 代理，是为了避开服务器自签证书的校验问题——
// 这条通道本来就已加密，不必再套一层 TLS。
func sshFetcher(req Request, emit Emitter) proxy.Fetcher {
	step := string(StepClient)
	return func(ctx context.Context, rawURL, dest string) error {
		emit(Event{Kind: KindLog, Step: step, Msg: "经服务器中转下载转发程序（约 17 MB）…"})

		ssh, err := remote.DialSSH(remote.SSHConfig{
			Host:     req.ServerHost,
			Port:     req.SSHPort,
			User:     req.SSHUser,
			Password: req.SSHPassword,
			Key:      req.SSHKey,
		})
		if err != nil {
			return err
		}
		defer ssh.Close()

		if _, err := ssh.Run("mkdir -p /tmp/dukou"); err != nil {
			return err
		}
		if _, err := ssh.Run(fmt.Sprintf(
			`curl -fsSL --retry 3 --connect-timeout 10 --max-time 300 -o /tmp/dukou/gost.zip %s`,
			shellQuote(rawURL))); err != nil {
			return fmt.Errorf("服务器下载 gost 失败（它可能访问不了 GitHub）: %w", err)
		}

		data, err := ssh.ReadFile("/tmp/dukou/gost.zip")
		if err != nil {
			return err
		}
		emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf(
			"已取回 %.1f MB", float64(len(data))/(1<<20))})

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	}
}

// dataDir 返回本工具的数据目录，与 internal/app 的约定保持一致。
func dataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录：%v", err)
	}
	return filepath.Join(base, "dukou"), nil
}
