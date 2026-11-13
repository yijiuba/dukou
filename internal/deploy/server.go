package deploy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yijiuba/dukou/internal/gost"
	"github.com/yijiuba/dukou/internal/remote"
	"github.com/yijiuba/dukou/internal/systune"
	"github.com/yijiuba/dukou/internal/systemd"
)

// setupServer 在海外服务器上装 gost 服务端并验证。
func setupServer(ctx context.Context, ssh *remote.SSH, info remote.OSInfo, req Request, emit Emitter) error {
	step := string(StepServer)

	// ── 服务器自身能否出网 ──────────────────────────────────────
	// 这一层最容易被忽略：把买在中国大陆地域的实例当出口，代理装得再漂亮，
	// 服务器自己也出不去，用户要等到最后一步才发现。放在最前面，一秒就能否掉。
	if info.HasCurl {
		emit(Event{Kind: KindLog, Step: step, Msg: "检查服务器能否直连海外…"})
		code, _ := ssh.Run(
			`curl -s -o /dev/null -w '%{http_code}' --max-time 12 https://api.openai.com/v1/models`)
		switch strings.TrimSpace(code) {
		case "401", "200":
			// 401 是没带凭据时的正常应答，证明 TCP、TLS、路由全通
			emit(Event{Kind: KindLog, Step: step, Msg: "服务器可直连 OpenAI"})
		default:
			return fmt.Errorf("服务器连不上 OpenAI（返回 %q）。这台机器很可能在中国大陆地域，"+
				"或者被安全策略挡住了外网。请换成海外地域的服务器"+
				"（新加坡、中国香港、日本、美国都可以）再试", strings.TrimSpace(code))
		}
	}

	// ── 端口占用 ────────────────────────────────────────────────
	emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf("检查 :%d 是否被占用", req.Port)})
	occ, _ := ssh.Run(fmt.Sprintf(
		`ss -lnt 2>/dev/null | awk 'NR>1{print $4}' | grep -c ':%d$' || true`, req.Port))
	if n, _ := strconv.Atoi(strings.TrimSpace(occ)); n > 0 {
		// 占着端口的可能是它自己：用户重跑一次本工具，上一次装的 dukou.service
		// 还在监听。这时候报"端口被占用"毫无意义，只会把人堵在门外。
		unitName := systemd.ServiceName("server")
		own, _ := ssh.Run(fmt.Sprintf(
			`[ -f /etc/systemd/system/%s.service ] && echo own || echo other`, unitName))
		if strings.TrimSpace(own) != "own" {
			return fmt.Errorf("端口 %d 已被占用，占用者不是本工具。请换一个端口"+
				"（高级设置里改），或先停掉那个程序：ss -lntp | grep :%d",
				req.Port, req.Port)
		}
		emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf(
			"检测到已安装的 %s，将覆盖配置并重启", unitName)})
	}

	// ── 安装 gost ───────────────────────────────────────────────
	channel := gost.ChannelFor(info.Family)
	url, err := gostURL(info.Arch, channel.Ext)
	if err != nil {
		return err
	}
	emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf(
		"下载 gost v%s（%s 通道）", gost.DefaultVersion, channel.Kind)})

	if _, err := ssh.Run("mkdir -p /tmp/dukou"); err != nil {
		return err
	}
	if _, err := ssh.Run(fmt.Sprintf(
		`curl -fsSL --retry 3 --connect-timeout 10 -o /tmp/dukou/gost%s %s`,
		channel.Ext, shellQuote(url))); err != nil {
		return fmt.Errorf("下载 gost 失败（服务器访问 GitHub 可能被阻断）: %w", err)
	}

	install, err := installCommand(channel.Kind, channel.Ext)
	if err != nil {
		return err
	}
	if _, err := ssh.Run(install); err != nil {
		return fmt.Errorf("安装 gost 失败: %w", err)
	}

	binPath, err := ssh.Run("command -v gost")
	if err != nil {
		return fmt.Errorf("装完了但找不到 gost 命令，安装通道可能不匹配")
	}
	binPath = strings.TrimSpace(binPath)
	emit(Event{Kind: KindLog, Step: step, Msg: "gost 已安装到 " + binPath})

	// ── 内核调优 ────────────────────────────────────────────────
	// 不调这个，交付的是"能连上但只有几十 KB/s"的东西。
	// 失败不阻断：代理本身仍然可用，只是慢，如实告知即可。
	emit(Event{Kind: KindLog, Step: step, Msg: "调优内核 TCP 参数…"})
	if msg, err := systune.Apply(ssh, false); err != nil {
		emit(Event{Kind: KindLog, Step: step, Msg: "内核调优未生效（代理仍可用，但速度会明显偏低）：" + err.Error()})
	} else {
		emit(Event{Kind: KindLog, Step: step, Msg: "拥塞算法 " + msg})
	}

	// ── systemd 单元 ────────────────────────────────────────────
	unit := serverUnit(req, binPath)
	if _, err := ssh.Run(writeFileCommand(unit.Path(), string(unit.Render()), false)); err != nil {
		return fmt.Errorf("写入 systemd 单元失败: %w", err)
	}
	// enable 只负责开机自启；restart 才是让新配置生效的那一步。
	// 用 enable --now 的话，服务已经在跑就不会重启，密码改了却还是旧的——
	// 这正是手工排障时最坑的一种"看起来改好了"。
	if _, err := ssh.Run(fmt.Sprintf(
		"systemctl daemon-reload && systemctl enable %s && systemctl restart %s",
		unit.Name, unit.Name)); err != nil {
		return fmt.Errorf("启动服务失败: %w", err)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "服务已启动（开机自启）"})

	// ── 防火墙 ──────────────────────────────────────────────────
	fw, _ := ssh.Run(fmt.Sprintf(`if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; then
  ufw allow %d/tcp >/dev/null && echo ufw
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port=%d/tcp >/dev/null && firewall-cmd --reload >/dev/null && echo firewalld
else
  echo none
fi`, req.Port, req.Port))
	switch strings.TrimSpace(fw) {
	case "ufw":
		emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf("ufw 已放行 %d/tcp", req.Port)})
	case "firewalld":
		emit(Event{Kind: KindLog, Step: step, Msg: fmt.Sprintf("firewalld 已放行 %d/tcp", req.Port)})
	default:
		emit(Event{Kind: KindLog, Step: step, Msg: "服务器内没有启用防火墙，跳过"})
	}

	// ── 服务端自检 ──────────────────────────────────────────────
	code, err := ssh.Run(fmt.Sprintf(
		`curl -sk -o /dev/null -w '%%{http_code}' --max-time 15 https://127.0.0.1:%d`, req.Port))
	if err != nil {
		return fmt.Errorf("服务端自检失败，服务可能没起来: %w", err)
	}
	if strings.TrimSpace(code) != "407" {
		return fmt.Errorf("服务端自检异常：期望 407（代理要求登录），实际 %s。"+
			"请检查 journalctl -u %s -n 30", strings.TrimSpace(code), unit.Name)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "服务端自检通过（407 = TLS 与代理层都正常）"})

	// ── 云安全组检查 ────────────────────────────────────────────
	// 系统防火墙和云安全组是两套东西：前者在机器里，后者在阿里云控制台。
	// 这一步从本机直接连服务器的对外端口，能区分到底是哪种没放开。
	emit(Event{Kind: KindLog, Step: step, Msg: "检查云安全组是否放行…"})
	local := remote.NewLocal(req.LocalSudo)
	probe := fmt.Sprintf(
		`timeout 8 bash -c 'exec 3<>/dev/tcp/%s/%d' && echo open || echo closed`,
		req.ServerHost, req.Port)
	out, _ := local.Run(probe)
	if !strings.Contains(out, "open") {
		return fmt.Errorf("服务器本机监听正常，但从公网连不上 %s:%d —— "+
			"这是云安全组没放行。请到阿里云控制台 → 服务器实例 → 安全组 → "+
			"入方向 → 手动添加：协议 TCP、端口 %d、授权对象 0.0.0.0/0，保存后重试",
			req.ServerHost, req.Port, req.Port)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "安全组已放行"})

	return nil
}

// installCommand 按通道生成安装命令。
func installCommand(kind, ext string) (string, error) {
	switch kind {
	case "deb":
		// dpkg 缺依赖时退回 apt 处理，阿里云镜像里都有
		return `dpkg -i /tmp/dukou/gost.deb || apt-get install -y /tmp/dukou/gost.deb`, nil
	case "rpm":
		return `rpm -Uvh --replacepkgs /tmp/dukou/gost.rpm || dnf install -y /tmp/dukou/gost.rpm`, nil
	case "tarball":
		return `tar -xzf /tmp/dukou/gost.tar.gz -C /tmp/dukou && ` +
			`install -m 0755 /tmp/dukou/gost /usr/local/bin/gost`, nil
	}
	return "", fmt.Errorf("未知安装通道 %q", kind)
}
