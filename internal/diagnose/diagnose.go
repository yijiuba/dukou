// Package diagnose 把本次人工排障的判断逻辑编码成可复用的函数。
//
// 这个包里的每个函数都对应一次真实的误诊：
//
//	ClassifyProxyResult      —— 407 被 -s 吞成 000，误判为断网
//	AnalyzeDomainSpecific    —— github 通但 google 不通，说明明文域名被识别
//	AnalyzeThroughput        —— 丢包 13% + 155ms 下 23KB/s，差点误判为带宽不足
//	DiagnoseTLSUpstream      —— -F 写成 http+tls:// 导致全站 503
//
// 把这些判断抽成纯函数，一方面能单元测试，另一方面让 doctor 的输出
// 「给结论」而不是「列一堆数字让用户自己猜」。
package diagnose

import (
	"fmt"
	"strconv"
	"strings"
)

// Status 是单项检查的结果。
type Status int

const (
	Pass Status = iota
	Fail
	Warn
	Skip
)

func (s Status) Symbol() string {
	switch s {
	case Pass:
		return "[ ok ]"
	case Fail:
		return "[fail]"
	case Warn:
		return "[warn]"
	}
	return "[skip]"
}

// Verdict 是一项检查的完整结论。
type Verdict struct {
	Status Status
	// Summary 是一句话结论，直接告诉用户「是什么问题」。
	Summary string
	// Explain 解释为什么这样判断，便于用户理解而不是盲从。
	Explain string
	// Fix 是可直接执行的修复命令。
	Fix []string
}

// ClassifyProxyResult 根据经代理访问的 HTTP 状态码判断链路状态。
//
// 这个函数的每个分支都来自实际遇到的情况，注释里写了当时的真实表现：
//
//	407 → 两端密码不一致。当时因为 curl 用了 -s（而非 -sS），
//	      错误信息被吞掉，只剩空输出和 000，被误判为网络不通。
//	503 → gost 服务端自己发的错，说明它连不上目标。
//	      客户端 -F 写成 http+tls:// 时会出现，所有站点一起 503。
//	403 → 目标站的风控，但链路本身是通的，不要在这上面浪费时间。
func ClassifyProxyResult(code string) Verdict {
	switch code {
	case "200", "204", "301", "302", "304":
		return Verdict{
			Status:  Pass,
			Summary: "链路正常（HTTP " + code + "）",
		}

	case "407":
		return Verdict{
			Status:  Fail,
			Summary: "代理拒绝鉴权：两端的用户名或密码不一致",
			Explain: "407 是代理层返回的，说明 TCP 与 TLS 都通、请求确实到达了代理，" +
				"问题只在凭据上。排查时先逐字符核对两端密码，不要去看网络。",
			Fix: []string{
				"# 服务端与客户端各查一次，密码必须完全一致",
				"grep -o 'tls://[^ ]*' /etc/systemd/system/dukou.service",
				"grep -o 'tls://[^ ]*' /etc/systemd/system/dukou-client.service",
				"# 改完重启两端",
				"systemctl restart dukou && systemctl restart dukou-client",
			},
		}

	case "403":
		return Verdict{
			Status:  Warn,
			Summary: "目标站拒绝（HTTP 403），但代理链路是通的",
			Explain: "403 来自目标服务器而非代理，通常是 Cloudflare 对机房 IP 的风控。" +
				"这不会影响其他站点，也不需要改代理配置。",
		}

	case "503":
		return Verdict{
			Status:  Fail,
			Summary: "代理服务端无法连到目标（HTTP 503 由 gost 自己发出）",
			Explain: "503 是 gost 报的，不是目标站报的。两种情况最常见：" +
				"客户端 -F 写法错误（必须写 tls://，不能写 http+tls://），" +
				"或服务端自身出网异常。",
			Fix: []string{
				"# 1. 确认客户端上游写法（必须是 -F tls://）",
				"grep ExecStart /etc/systemd/system/dukou-client.service",
				"# 2. 确认服务端自己能出网",
				"curl -sS -o /dev/null -w '%{http_code}\\n' https://api.openai.com/v1/models",
			},
		}

	case "000", "":
		return Verdict{
			Status:  Fail,
			Summary: "连接没有建立（curl 返回 000/空）",
			Explain: "注意 000 不等于「网络不通」：用 curl -s 时错误信息会被吞掉，" +
				"407 之类的响应也会表现为 000。所以先确认是不是用了 -sS 而不是 -s。",
			Fix: []string{
				"# 用 -sS 而不是 -s，让错误信息显出来",
				"curl -x http://127.0.0.1:7890 -sS https://api.ipify.org",
				"# 再看本地中转是否在跑",
				"systemctl is-active dukou-client",
			},
		}
	}

	return Verdict{
		Status:  Warn,
		Summary: "非预期状态码 " + code,
	}
}

// DomainResult 是单个域名的探测结果。
type DomainResult struct {
	Host string
	Code string
}

// AnalyzeDomainSpecific 判断「部分域名通、部分不通」这一现象的根因。
//
// 这是本次排障中最关键的一次判断。当时的观测是：
//
//	github.com   200
//	google.com   000（25ms 内被 RST）
//	chatgpt.com  000
//	ECS 本机 curl chatgpt → 200
//
// 同一条链路、同一个代理，只有部分域名失败，且失败是「立即 RST」而不是超时
// —— 这说明不是 DNS、不是 MTU、不是带宽，而是明文 CONNECT 里的域名被识别。
func AnalyzeDomainSpecific(results []DomainResult) Verdict {
	if len(results) == 0 {
		return Verdict{Status: Skip, Summary: "没有可分析的探测结果"}
	}

	var ok, bad []string
	for _, r := range results {
		switch r.Code {
		case "200", "204", "301", "302", "304", "403":
			// 403 也算通：目标站应答了，只是拒绝了这个 IP 或请求
			ok = append(ok, r.Host)
		default:
			bad = append(bad, r.Host)
		}
	}

	if len(bad) == 0 {
		return Verdict{
			Status:  Pass,
			Summary: fmt.Sprintf("全部 %d 个测试域名均可达", len(results)),
		}
	}

	if len(ok) == 0 {
		return Verdict{
			Status:  Fail,
			Summary: "所有测试域名都不通，说明是链路或代理整体故障",
			Explain: "这种模式与「域名被针对性阻断」不同：全都不通通常是代理没运行、" +
				"端口没放行、或凭据错误。先回到上一层检查。",
			Fix: []string{
				"systemctl is-active dukou-client",
				"curl -x http://127.0.0.1:7890 -sS https://api.ipify.org",
			},
		}
	}

	// 有通有不通 —— 这正是明文域名被识别的特征
	return Verdict{
		Status: Fail,
		Summary: fmt.Sprintf("%d 个域名可达，但 %s 不通：典型的明文 CONNECT 域名被识别",
			len(ok), strings.Join(bad, "、")),
		Explain: "同一链路下只有部分域名失败，且失败通常是「立即 RST」而非超时，" +
			"说明中间设备读到了明文 HTTP CONNECT 请求里的域名并主动切断。" +
			"这与 DNS、MTU、带宽都无关——ECS 本机 curl 同一个域名往往是通的。" +
			"解决办法是把代理连接包进 TLS，让域名不可见。",
		Fix: []string{
			"# 服务端改用 http+tls 监听",
			"grep ExecStart /etc/systemd/system/dukou.service   # 应含 http+tls://",
			"# 客户端上游改用 tls://",
			"grep ExecStart /etc/systemd/system/dukou-client.service   # 应含 -F tls://",
			"# 两者都改好后重启",
			"systemctl restart dukou && systemctl restart dukou-client",
		},
	}
}

// PerfInput 是一次性能探测的输入。
type PerfInput struct {
	// BytesPerSecond 是实测下载速度
	BytesPerSecond float64
	// LossPct 是 ping 测得的丢包百分比
	LossPct float64
	// RTTMs 是往返延迟毫秒
	RTTMs float64
	// CongestionControl 是内核当前拥塞算法，如 cubic / bbr
	CongestionControl string
}

// AnalyzeThroughput 判断「慢」的真正原因。
//
// 这里编码了一条代价很大的经验：跨境链路 155ms + 13% 丢包时，
// 单流吞吐只有 23KB/s。当时以为是带宽不足，差点让用户去升级带宽。
// 实际上用 TCP 吞吐近似公式反算：
//
//	吞吐 ≈ (MSS / RTT) × (1 / √丢包率) = (1500/0.155) × (1/√0.13) ≈ 26KB/s
//
// 与实测几乎完全吻合，说明瓶颈是丢包而非带宽。开 BBR 后速度提升 71 倍，
// 带宽一个字节都没加。
func AnalyzeThroughput(in PerfInput) Verdict {
	kb := in.BytesPerSecond / 1024

	// 先用公式算出「丢包理论值」用于对照
	var predictedKB float64
	if in.RTTMs > 0 && in.LossPct > 0 {
		mss := 1500.0
		rttSec := in.RTTMs / 1000
		predictedKB = (mss / rttSec) * (1 / sqrt(in.LossPct/100)) / 1024
	}

	switch {
	case kb >= 200:
		return Verdict{
			Status: Pass,
			Summary: fmt.Sprintf("吞吐 %.0f KB/s（约 %.1f Mbps），链路健康",
				kb, kb*8/1024),
		}

	case kb >= 50:
		return Verdict{
			Status: Warn,
			Summary: fmt.Sprintf("吞吐 %.0f KB/s，偏慢但可用", kb),
			Explain: "这个区间通常还能再优化。先看拥塞算法是否为 bbr。" +
				"Codex 这类 KB 级请求完全够用，浏览器打开网页会略慢。",
		}
	}

	// 慢：区分是丢包造成还是带宽上限
	if in.LossPct >= 3 {
		explain := fmt.Sprintf("实测 %.0f KB/s，而丢包 %.1f%%、RTT %.0f ms。", kb, in.LossPct, in.RTTMs)
		if predictedKB > 0 {
			explain += fmt.Sprintf("按 TCP 吞吐公式 (MSS/RTT)×(1/√丢包) 反算约为 %.0f KB/s，"+
				"与实测基本吻合——说明瓶颈是丢包，不是带宽。", predictedKB)
		}
		explain += "这种情况下升级带宽没有用，必须换拥塞控制算法。"

		fix := []string{
			"# 两端都要开 BBR（发送端最关键）",
			"modprobe tcp_bbr && echo tcp_bbr > /etc/modules-load.d/bbr.conf",
			"printf 'net.core.default_qdisc=fq\\nnet.ipv4.tcp_congestion_control=bbr\\n' >> /etc/sysctl.conf",
			"sysctl -p && sysctl net.ipv4.tcp_congestion_control   # 应显示 bbr",
		}
		if in.CongestionControl == "bbr" {
			explain += "当前已是 bbr，若仍然慢，说明线路丢包过于严重，考虑换地域或改用 Hysteria2。"
			fix = []string{
				"# BBR 已开启仍慢，下一步考虑换协议（QUIC 抗丢包）",
				"# 或换到新加坡/日本地域，降低 RTT 与丢包",
			}
		}

		return Verdict{
			Status:  Fail,
			Summary: fmt.Sprintf("吞吐 %.0f KB/s：丢包 %.1f%% 是瓶颈，不是带宽", kb, in.LossPct),
			Explain: explain,
			Fix:     fix,
		}
	}

	return Verdict{
		Status: Warn,
		Summary: fmt.Sprintf("吞吐 %.0f KB/s，但丢包只有 %.1f%%", kb, in.LossPct),
		Explain: "丢包不高却慢，通常是带宽上限或单流窗口限制。去云控制台看实例的" +
			"带宽规格与计费方式；若带宽充足，则可能是 gost 单连接的转发效率问题。",
		Fix: []string{
			"# 在服务端本机测一次，绕开转发层做对照",
			"curl -o /dev/null -sS -w '%{speed_download}\\n' https://speed.cloudflare.com/__down?bytes=10000000",
		},
	}
}

// ParseSpeedOutput 解析 curl -w 输出的纯数字。
func ParseSpeedOutput(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// sqrt 是 Newton 迭代，避免为此引入 math 的浮点细节差异。
// 精度对判断结论完全够用（结果只用于显示对照值）。
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x / 2
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
