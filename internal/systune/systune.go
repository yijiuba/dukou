// Package systune 负责内核 TCP 参数调优。
//
// 为什么这件事必须内置：中国大陆到海外这条链路普遍有 10%~20% 的丢包，
// 传统 cubic 一遇丢包就把窗口砍半，吞吐会塌到几十 KB/s。实测同一台机器
// 开 BBR 前后是 23 KB/s 与 1.65 MB/s，差 71 倍——不是带宽问题，
// 是拥塞算法不认识"丢包不等于拥塞"。
//
// 一个只装代理、不调内核的工具，交付的是一个能连上但没法用的东西。
package systune

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/yijiuba/dukou/internal/remote"
)

// ConfPath 是写入的 sysctl 配置文件。带 99 前缀是为了排在发行版自己的
// 配置之后，确保我们的值最后生效。
const ConfPath = "/etc/sysctl.d/99-dukou.conf"

// ModulePath 保证 tcp_bbr 在开机时被加载。
//
// Alibaba Cloud Linux 8/4 这类系统把 bbr 编成模块且默认不加载，
// 只写 sysctl 而不写这里，重启后会静默退回 cubic——用户完全察觉不到。
const ModulePath = "/etc/modules-load.d/dukou-bbr.conf"

// Conf 渲染 sysctl 配置内容。
//
// 缓冲区的意义：BBR 要靠足够大的窗口才能跑满带宽延迟积（BDP）。
// 一条 200Mbps、RTT 200ms 的链路需要约 5MB 在途数据，
// 默认的几百 KB 窗口会把速度死死按在几 MB/s 以下。
func Conf() string {
	return strings.Join([]string{
		"# 由 dukou 写入。改完执行 sysctl --system 生效。",
		"# 拥塞控制：bbr 代替 cubic，丢包时不再无脑砍窗口",
		"net.core.default_qdisc = fq",
		"net.ipv4.tcp_congestion_control = bbr",
		"",
		"# 缓冲区：按 200Mbps × 200ms 的带宽延迟积留余量",
		"net.core.rmem_max = 67108864",
		"net.core.wmem_max = 67108864",
		"net.ipv4.tcp_rmem = 4096 87380 67108864",
		"net.ipv4.tcp_wmem = 4096 65536 67108864",
		"",
		"# 路径 MTU 自动探测：跨国链路中途常有不同 MTU 的设备",
		"net.ipv4.tcp_mtu_probing = 1",
		"",
		"# 三次握手时就开始传数据，省一个 RTT，对短请求帮助明显",
		"net.ipv4.tcp_fastopen = 3",
	}, "\n") + "\n"
}

// Apply 在目标机器上启用 BBR，并回读确认真的生效了。
//
// sudo 为 true 时每条命令都加 sudo 前缀——本机以普通用户运行图形界面时
// 走这条路；服务器上是 root，传 false。
// 返回的说明文字用于界面日志，例如 "bbr（已回读确认）"。
func Apply(e remote.Exec, sudo bool) (string, error) {
	pre := ""
	if sudo {
		pre = "sudo "
	}
	// 1) 先尝试加载模块。内核已内建 bbr 时 modprobe 会失败，属正常情况，
	//    所以这里不能用 && 串联，否则后面全被跳过。
	if _, err := e.Run(pre + "modprobe tcp_bbr 2>/dev/null || true"); err != nil {
		return "", err
	}

	// 2) 写配置。用 base64 传内容，避免 heredoc 与引号在远端被 shell 吃掉。
	if _, err := e.Run(pre + writeCmd(ConfPath, Conf())); err != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", ConfPath, err)
	}
	if _, err := e.Run(pre + writeCmd(ModulePath, "tcp_bbr\n")); err != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", ModulePath, err)
	}

	// 3) 应用。sysctl --system 会按目录顺序重放所有配置。
	if out, err := e.Run(pre + "sysctl --system 2>&1 | tail -3"); err != nil {
		return "", fmt.Errorf("应用 sysctl 配置失败: %s", strings.TrimSpace(out))
	}

	// 4) 回读。写进去不等于生效——模块没加载时内核会忽略这一项且不报错，
	//    只写不读是这类脚本最常见的假成功。
	out, err := e.Run(pre + "sysctl -n net.ipv4.tcp_congestion_control")
	if err != nil {
		return "", fmt.Errorf("回读拥塞算法失败: %w", err)
	}
	got := strings.TrimSpace(out)
	if got != "bbr" {
		return "", fmt.Errorf("已写入配置但当前拥塞算法仍是 %q（内核可能不支持 bbr）", got)
	}
	return "bbr（已回读确认）", nil
}

// writeCmd 生成把内容写入文件的命令。
func writeCmd(path, content string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf("echo '%s' | base64 -d > %s", enc, path)
}
