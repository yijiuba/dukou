package deploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"

	"github.com/yijiuba/dukou/internal/gost"
	"github.com/yijiuba/dukou/internal/remote"
)

// setupClient 在本机（通常是云电脑）装 gost 客户端、配好代理并自检。
// 返回实测出口 IP，用于证明流量确实走了海外服务器。
func setupClient(ctx context.Context, local *remote.Local, req Request, emit Emitter) (string, error) {
	step := string(StepClient)
	port := 7890

	// ── 权限 ────────────────────────────────────────────────────
	// 装 systemd 单元需要 root，而图形界面一般以普通用户运行。
	// 这里先试探一次，失败就把话说清楚，别等到写文件时才报错。
	emit(Event{Kind: KindLog, Step: step, Msg: "检查本机权限…"})
	if _, err := local.Run("sudo true"); err != nil {
		return "", fmt.Errorf("本机 sudo 权限不可用。请在「本机密码」里填你的登录密码"+
			"（安装服务需要管理员权限）: %w", err)
	}

	// ── 安装 gost 客户端 ────────────────────────────────────────
	localArch := runtime.GOARCH
	channel := gost.ChannelFor("unknown") // 本机统一走静态二进制，最稳
	url, err := gostURL(localArch, channel.Ext)
	if err != nil {
		return "", fmt.Errorf("本机架构 %s 不受支持: %w", localArch, err)
	}

	emit(Event{Kind: KindLog, Step: step, Msg: "下载 gost 客户端…"})
	if _, err := local.Run(fmt.Sprintf(
		`mkdir -p /tmp/dukou && curl -fsSL --retry 3 --connect-timeout 10 -o /tmp/dukou/gost.tar.gz %s`,
		shellQuote(url))); err != nil {
		return "", fmt.Errorf("下载 gost 失败（本机访问 GitHub 可能不通）: %w", err)
	}
	if _, err := local.Run(
		`tar -xzf /tmp/dukou/gost.tar.gz -C /tmp/dukou && ` +
			`sudo install -m 0755 /tmp/dukou/gost /usr/local/bin/gost`); err != nil {
		return "", fmt.Errorf("安装 gost 失败: %w", err)
	}

	// ── systemd 单元 ────────────────────────────────────────────
	unit := clientUnit(req, "/usr/local/bin/gost")
	if _, err := local.Run(writeFileCommand(unit.Path(), string(unit.Render()), true)); err != nil {
		return "", fmt.Errorf("写入服务配置失败: %w", err)
	}
	if _, err := local.Run(fmt.Sprintf(
		"sudo systemctl daemon-reload && sudo systemctl enable --now %s", unit.Name)); err != nil {
		return "", fmt.Errorf("启动本机代理服务失败: %w", err)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "本机代理服务已启动（127.0.0.1:" + fmt.Sprint(port) + "）"})

	// ── 系统代理 ────────────────────────────────────────────────
	// 系统代理只影响图形界面程序（浏览器）；Codex 这类命令行工具读的是
	// 环境变量，两处都要配，少一个就会"浏览器能开、Codex 连不上"。
	if _, err := local.Run(gnomeProxyScript(port)); err != nil {
		emit(Event{Kind: KindLog, Step: step, Msg: "跳过系统代理（当前不是 GNOME 桌面）"})
	} else {
		emit(Event{Kind: KindLog, Step: step, Msg: "系统代理已设置（Firefox 需选「使用系统代理设置」）"})
	}

	// ── 命令行环境变量 ──────────────────────────────────────────
	block := proxyEnvBlock()
	enc := base64.StdEncoding.EncodeToString([]byte(block))
	if _, err := local.Run(fmt.Sprintf(
		`grep -q '>>> dukou proxy >>>' "$HOME/.bashrc" || echo '%s' | base64 -d >> "$HOME/.bashrc"`, enc)); err != nil {
		return "", fmt.Errorf("写入 ~/.bashrc 失败: %w", err)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "已写入命令行代理变量（新开终端生效）"})

	// ── 自检：先确认代理链路，再确认出口 IP ─────────────────────
	emit(Event{Kind: KindLog, Step: step, Msg: "自检代理链路…"})
	code, err := local.Run(fmt.Sprintf(
		`curl -sS -x http://127.0.0.1:%d -o /dev/null -w '%%{http_code}' --max-time 25 https://api.github.com`, port))
	if err != nil {
		return "", fmt.Errorf("代理自检失败: %w", err)
	}
	if got := strings.TrimSpace(code); got != "200" {
		return "", fmt.Errorf("代理已启动但连不通外网（返回 %s）。"+
			"常见原因：服务器端密码与此处不一致，或 8443 端口被中间设备重置。"+
			"排查：sudo journalctl -u %s -n 30", got, unit.Name)
	}

	ip, err := local.Run(fmt.Sprintf(
		`curl -sS -x http://127.0.0.1:%d --max-time 25 https://api.ipify.org`, port))
	if err != nil {
		return "", fmt.Errorf("拿不到出口 IP: %w", err)
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("出口 IP 为空，代理可能没生效")
	}

	return ip, nil
}

// gnomeProxyScript 生成设置 GNOME 系统代理的命令序列。
//
// 忽略列表里放的是"不该出国"的目标：国内域名后缀、阿里云内网、
// 私有网段。少了阿里云内网最容易踩坑——apt 走 mirror 时会莫名 503。
func gnomeProxyScript(port int) string {
	return fmt.Sprintf(`
command -v gsettings >/dev/null 2>&1 || exit 1
gsettings set org.gnome.system.proxy mode 'manual' || exit 1
gsettings set org.gnome.system.proxy.http host '127.0.0.1'
gsettings set org.gnome.system.proxy.http port %d
gsettings set org.gnome.system.proxy.https host '127.0.0.1'
gsettings set org.gnome.system.proxy.https port %d
gsettings set org.gnome.system.proxy.socks host '127.0.0.1'
gsettings set org.gnome.system.proxy.socks port 7891
gsettings set org.gnome.system.proxy ignore-hosts "['localhost','127.0.0.0/8','::1','*.cn','*.com.cn','*.aliyun.com','*.aliyuncs.com','*.taobao.com','10.0.0.0/8','172.16.0.0/12','192.168.0.0/16']"
`, port, port)
}

// proxyEnvBlock 是写进 ~/.bashrc 的代理环境变量块。
// 用明显的首尾标记包裹，重复执行不会叠加，卸载时也能整段删掉。
func proxyEnvBlock() string {
	return `
# >>> dukou proxy >>>
export HTTP_PROXY="http://127.0.0.1:7890"
export HTTPS_PROXY="$HTTP_PROXY"
export http_proxy="$HTTP_PROXY"
export https_proxy="$HTTP_PROXY"
export NO_PROXY="localhost,127.0.0.1,::1,.cn,.aliyun.com,.aliyuncs.com,.taobao.com,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
export no_proxy="$NO_PROXY"
# <<< dukou proxy <<<
`
}
